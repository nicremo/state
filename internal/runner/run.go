package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/nicremo/state/internal/state"
	"github.com/nicremo/state/internal/statectl"
)

// defaultHeartbeatInterval paces lease heartbeats while an adapter runs.
const defaultHeartbeatInterval = 30 * time.Second

// maxResultSummaryLength mirrors the server-side bound on result summaries.
const maxResultSummaryLength = 2000

// secretLinePattern matches credential-looking output lines. The runner
// strips them before any summary crosses the wire; the server redacts again.
var secretLinePattern = regexp.MustCompile(`(?i)(key|token|secret|password)\s*[:=]\s*\S+`)

// Runner drives the claim → validate → launch → report loop. Logs and adapter
// output stay local; only redacted summaries and lifecycle events cross the
// wire.
type Runner struct {
	Config            RunnerConfig
	Client            *Client
	Adapters          map[string]Adapter
	HeartbeatInterval time.Duration
	// Log receives human-readable status notes (stderr in the binary).
	Log io.Writer
}

// Run polls for work until the context ends. With once=true it performs one
// claim attempt (no long-poll) and returns after that cycle, for tests and
// CI. A rejected credential (401) is terminal: re-pair instead of retrying.
func (runner *Runner) Run(ctx context.Context, once bool) error {
	for {
		claimed, err := runner.claimAndExecute(ctx, once)
		if once {
			return err
		}
		if errors.Is(err, ErrUnauthorized) {
			return fmt.Errorf("runner credential was rejected — re-run state-runner pair: %w", err)
		}
		if err != nil {
			runner.note("cycle failed: %v", err)
		}
		if !claimed || err != nil {
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(time.Duration(runner.Config.PollIntervalSeconds) * time.Second):
			}
		}
	}
}

// claimAndExecute claims one run and, when one was claimed, executes it to a
// terminal report.
func (runner *Runner) claimAndExecute(ctx context.Context, once bool) (bool, error) {
	wait := runner.Config.LongPollSeconds
	if once {
		wait = 0
	}
	run, err := runner.Client.Claim(ctx, wait)
	if errors.Is(err, state.ErrNotClaimable) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if run.Status == state.AgentRunStatusCancelled {
		return true, nil
	}
	return true, runner.execute(ctx, run)
}

// execute runs one claimed contract to a terminal state. Validation and
// adapter problems are reported as failed runs; only infrastructure errors
// (claim/report transports) bubble up to the loop.
func (runner *Runner) execute(ctx context.Context, run state.AgentRun) error {
	revision := run.Revision
	checkoutDir, err := runner.validateRun(run)
	if err != nil {
		runner.note("run %s rejected: %v", run.ID, err)
		return runner.rejectRun(ctx, run, revision, err.Error())
	}
	runner.note("run %s claimed: %s (%s via %s)", run.ID, run.TaskContract.Objective, run.TaskContract.ProjectName, run.Adapter)

	runDir := filepath.Join(checkoutDir, ".state", "runs", run.ID)
	if err := writeRunFiles(runDir, run); err != nil {
		return fmt.Errorf("write run files: %w", err)
	}
	contextNote := runner.refreshContext(ctx, checkoutDir, run)

	updated, err := runner.Client.ReportEvent(ctx, run.ID, state.RunEventStarted, "adapter "+run.Adapter+" launching", revision)
	if err != nil {
		if errors.Is(err, state.ErrRunStateConflict) || errors.Is(err, state.ErrForbidden) {
			runner.note("run %s changed state before launch: %v", run.ID, err)
			return nil
		}
		return fmt.Errorf("report started: %w", err)
	}
	if updated.Status == state.AgentRunStatusCancelled {
		runner.note("run %s was cancelled before launch", run.ID)
		return nil
	}
	revision = updated.Revision

	adapter := runner.Adapters[run.Adapter]
	if adapter == nil {
		return runner.failUnavailable(ctx, run, revision, fmt.Sprintf("adapter %q is not registered in this runner", run.Adapter))
	}
	if err := adapter.Validate(run.TaskContract); err != nil {
		return runner.failUnavailable(ctx, run, revision, err.Error())
	}

	shared := &runExecution{revision: revision}
	session, err := adapter.Start(ctx, StartRequest{
		Contract: run.TaskContract,
		Dir:      checkoutDir,
		Prompt:   BuildPrompt(run.TaskContract),
	})
	if err != nil {
		return runner.failUnavailable(ctx, run, shared.Revision(), fmt.Sprintf("start adapter: %v", err))
	}
	tailFn := func() string {
		if process, ok := session.(*processSession); ok {
			return process.tail.String()
		}
		return ""
	}
	writeStatus := func(heartbeatRun state.AgentRun) {
		if err := writeStatusFile(runDir, heartbeatRun, "running", "", nil, tailFn(), contextNote); err != nil {
			runner.note("run %s: write status: %v", run.ID, err)
		}
	}
	writeStatus(updated)
	heartbeats, heartbeatsDone := runner.startHeartbeat(ctx, session, run.ID, shared, writeStatus)
	result, _ := session.Wait(ctx)
	close(heartbeats)
	<-heartbeatsDone

	finalStatus := "succeeded"
	detail := ""
	if shared.Cancelled() {
		finalStatus = "cancelled"
		detail = "cancelled by owner"
	} else if result.ExitCode != 0 {
		finalStatus = "failed"
	}
	_ = writeStatusFile(runDir, run, finalStatus, detail, &result.ExitCode, result.Tail, contextNote)

	summary := summarizeResult(result)
	if shared.Cancelled() {
		summary = "cancelled by owner"
	}
	outcome := state.AgentRunStatusSucceeded
	if finalStatus != "succeeded" {
		outcome = state.AgentRunStatusFailed
	}
	_, completeErr := runner.Client.Complete(ctx, state.CompleteRunInput{
		RunID:             run.ID,
		Outcome:           outcome,
		ResultSummary:     summary,
		ResultArtifactRef: filepath.Join(".state", "runs", run.ID),
		ExitCode:          result.ExitCode,
		ExpectedRevision:  shared.Revision(),
		MutationMetadata: state.MutationMetadata{
			ClientRequestID: uuid.NewString(),
			Source:          "state-runner",
		},
	})
	switch {
	case completeErr == nil:
		runner.note("run %s %s (exit %d)", run.ID, outcome, result.ExitCode)
	case errors.Is(completeErr, state.ErrRunStateConflict):
		// The server already moved the run to a terminal state (for example
		// owner cancellation). Nothing left to report.
		runner.note("run %s already terminal server-side", run.ID)
	default:
		return fmt.Errorf("complete run %s: %w", run.ID, completeErr)
	}
	return nil
}

// validateRun checks the contract hash, the policy revision pin, the runner's
// project and adapter scopes, and working-directory containment. It returns
// the verified checkout directory.
func (runner *Runner) validateRun(run state.AgentRun) (string, error) {
	contract := run.TaskContract
	if contract.RunID != run.ID {
		return "", errors.New("contract run_id does not match the claimed run")
	}
	if contract.ContractHash == "" || contract.ContractHash != contract.ComputeHash() {
		return "", errors.New("contract hash mismatch")
	}
	if contract.PolicyID != run.PolicyID || contract.PolicyRevision != run.PolicyRevision {
		return "", errors.New("policy revision pin mismatch")
	}
	if !containsString(runner.Config.Projects, contract.ProjectID) || contract.ProjectID != run.ProjectID {
		return "", fmt.Errorf("project %q is not served by this runner", contract.ProjectName)
	}
	if !containsString(runner.Config.Adapters, run.Adapter) {
		return "", fmt.Errorf("adapter %q is not configured on this runner", run.Adapter)
	}
	if !state.ValidProjectSlug(contract.ProjectName) {
		return "", fmt.Errorf("project name %q is not a safe directory name", contract.ProjectName)
	}

	root := filepath.Clean(runner.Config.WorkRoot)
	rootResolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", fmt.Errorf("resolve work root: %w", err)
	}
	checkout := filepath.Join(rootResolved, contract.ProjectName)
	info, err := os.Stat(checkout)
	if err != nil {
		return "", fmt.Errorf("project checkout %s is missing: %w", checkout, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("project checkout %s is not a directory", checkout)
	}
	checkoutResolved, err := filepath.EvalSymlinks(checkout)
	if err != nil {
		return "", fmt.Errorf("resolve project checkout: %w", err)
	}
	if checkoutResolved != rootResolved && !strings.HasPrefix(checkoutResolved, rootResolved+string(os.PathSeparator)) {
		return "", fmt.Errorf("project checkout %s escapes work root %s", checkoutResolved, rootResolved)
	}
	return checkoutResolved, nil
}

// rejectRun reports a contract that failed local validation. The run never
// reaches an adapter.
func (runner *Runner) rejectRun(ctx context.Context, run state.AgentRun, revision int64, reason string) error {
	if reported, err := runner.Client.ReportEvent(ctx, run.ID, state.RunEventProgress, "rejected: "+firstLine(reason), revision); err == nil {
		revision = reported.Revision
	}
	_, err := runner.Client.Complete(ctx, state.CompleteRunInput{
		RunID:            run.ID,
		Outcome:          state.AgentRunStatusFailed,
		ResultSummary:    redactSummary("rejected: " + firstLine(reason)),
		ExitCode:         1,
		ExpectedRevision: revision,
		MutationMetadata: state.MutationMetadata{
			ClientRequestID: uuid.NewString(),
			Source:          "state-runner",
		},
	})
	if err != nil && !errors.Is(err, state.ErrRunStateConflict) {
		return fmt.Errorf("report rejection of run %s: %w", run.ID, err)
	}
	return nil
}

// failUnavailable reports a run whose adapter cannot launch. The structured
// failure code travels in CompleteRunInput; the marker also stays in the
// redacted summary so older servers still surface it.
func (runner *Runner) failUnavailable(ctx context.Context, run state.AgentRun, revision int64, reason string) error {
	detail := string(ErrAdapterUnavailable.Error()) + ": " + firstLine(reason)
	if reported, err := runner.Client.ReportEvent(ctx, run.ID, state.RunEventProgress, detail, revision); err == nil {
		revision = reported.Revision
	}
	_, err := runner.Client.Complete(ctx, state.CompleteRunInput{
		RunID:            run.ID,
		Outcome:          state.AgentRunStatusFailed,
		ResultSummary:    redactSummary(detail),
		FailureCode:      state.RunFailureAdapterUnavailable,
		ExitCode:         1,
		ExpectedRevision: revision,
		MutationMetadata: state.MutationMetadata{
			ClientRequestID: uuid.NewString(),
			Source:          "state-runner",
		},
	})
	if err != nil && !errors.Is(err, state.ErrRunStateConflict) {
		return fmt.Errorf("report adapter_unavailable for run %s: %w", run.ID, err)
	}
	runner.note("run %s failed: %s", run.ID, detail)
	return nil
}

// refreshContext rewrites .state/context/current.md from the server briefing.
// It is best-effort: a failure becomes a note in status.json, never a launch
// blocker. Returns "" on success.
func (runner *Runner) refreshContext(ctx context.Context, checkoutDir string, run state.AgentRun) string {
	briefing, err := runner.Client.Briefing(ctx, 50)
	if err != nil {
		note := "context refresh skipped: " + firstLine(err.Error())
		runner.note("run %s: %s", run.ID, note)
		return note
	}
	project := statectl.ProjectFile{
		SchemaVersion: 1,
		ProjectID:     run.TaskContract.ProjectID,
		ProjectName:   run.TaskContract.ProjectName,
		Server:        runner.Config.ServerURL,
	}
	path := filepath.Join(checkoutDir, ".state", "context", "current.md")
	if err := statectl.WriteAtomic(path, []byte(statectl.RenderBriefingMarkdown(project, briefing)), 0o600); err != nil {
		note := "context refresh failed: " + firstLine(err.Error())
		runner.note("run %s: %s", run.ID, note)
		return note
	}
	return ""
}

// startHeartbeat extends the run lease every heartbeat interval while the
// adapter runs and refreshes the local status projection. A cancelled or
// conflicted run kills the process group. Close the returned stop channel and
// wait on the done channel before completing, so the last heartbeat revision
// is settled.
func (runner *Runner) startHeartbeat(ctx context.Context, session Session, runID string, shared *runExecution, afterBeat func(state.AgentRun)) (chan<- struct{}, <-chan struct{}) {
	stop := make(chan struct{})
	done := make(chan struct{})
	interval := runner.HeartbeatInterval
	if interval <= 0 {
		interval = defaultHeartbeatInterval
	}
	go func() {
		defer close(done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				_ = session.Cancel(context.Background())
				return
			case <-stop:
				return
			case <-ticker.C:
			}
			updated, err := runner.Client.ReportEvent(ctx, runID, state.RunEventHeartbeat, "", shared.Revision())
			if err != nil {
				if errors.Is(err, state.ErrRunStateConflict) || errors.Is(err, state.ErrForbidden) || errors.Is(err, state.ErrNotFound) || errors.Is(err, ErrUnauthorized) {
					runner.note("run %s is no longer ours (%v); killing the adapter", runID, err)
					shared.MarkCancelled()
					_ = session.Cancel(context.Background())
					return
				}
				runner.note("run %s heartbeat failed: %v", runID, err)
				continue
			}
			if updated.Status == state.AgentRunStatusCancelled {
				runner.note("run %s was cancelled by the owner; killing the adapter", runID)
				shared.MarkCancelled()
				_ = session.Cancel(context.Background())
				return
			}
			shared.SetRevision(updated.Revision)
			if afterBeat != nil {
				afterBeat(updated)
			}
		}
	}()
	return stop, done
}

// writeRunFiles persists the immutable contract and the initial status of a
// claimed run inside the checkout.
func writeRunFiles(runDir string, run state.AgentRun) error {
	contract, err := json.MarshalIndent(run.TaskContract, "", "  ")
	if err != nil {
		return err
	}
	if err := statectl.WriteAtomic(filepath.Join(runDir, "contract.json"), append(contract, '\n'), 0o600); err != nil {
		return err
	}
	return writeStatusFile(runDir, run, string(run.Status), "", nil, "", "")
}

// runStatusFile is the safe-to-regenerate local status projection of a run.
type runStatusFile struct {
	SchemaVersion  int        `json:"schema_version"`
	RunID          string     `json:"run_id"`
	Project        string     `json:"project"`
	Adapter        string     `json:"adapter"`
	Status         string     `json:"status"`
	Detail         string     `json:"detail,omitempty"`
	ExitCode       *int       `json:"exit_code,omitempty"`
	LeaseExpiresAt *time.Time `json:"lease_expires_at,omitempty"`
	UpdatedAt      time.Time  `json:"updated_at"`
	Note           string     `json:"note,omitempty"`
	OutputTail     string     `json:"output_tail,omitempty"`
}

func writeStatusFile(runDir string, run state.AgentRun, status string, detail string, exitCode *int, tail string, note string) error {
	file := runStatusFile{
		SchemaVersion:  1,
		RunID:          run.ID,
		Project:        run.TaskContract.ProjectName,
		Adapter:        run.Adapter,
		Status:         status,
		Detail:         detail,
		ExitCode:       exitCode,
		LeaseExpiresAt: run.LeaseExpiresAt,
		UpdatedAt:      time.Now().UTC(),
		Note:           note,
		OutputTail:     tail,
	}
	encoded, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return err
	}
	return statectl.WriteAtomic(filepath.Join(runDir, "status.json"), append(encoded, '\n'), 0o600)
}

// summarizeResult builds the redacted one-line completion summary from the
// bounded output tail and the exit code.
func summarizeResult(result Result) string {
	line := firstLine(redactSummary(result.Tail))
	summary := fmt.Sprintf("exit %d: %s", result.ExitCode, strings.TrimSpace(line))
	return redactSummary(summary)
}

// redactSummary drops secret-looking lines and bounds the text, mirroring the
// server-side rule so a summary is clean before it leaves the workstation.
func redactSummary(text string) string {
	lines := strings.Split(text, "\n")
	kept := make([]string, 0, len(lines))
	for _, line := range lines {
		if secretLinePattern.MatchString(line) {
			continue
		}
		kept = append(kept, line)
	}
	redacted := strings.Join(kept, "\n")
	runes := []rune(redacted)
	if len(runes) > maxResultSummaryLength {
		redacted = string(runes[:maxResultSummaryLength])
	}
	return redacted
}

func firstLine(text string) string {
	for _, line := range strings.Split(text, "\n") {
		if strings.TrimSpace(line) != "" {
			return line
		}
	}
	return ""
}

func containsString(values []string, candidate string) bool {
	for _, value := range values {
		if value == candidate {
			return true
		}
	}
	return false
}

func (runner *Runner) note(format string, args ...any) {
	if runner.Log == nil {
		return
	}
	fmt.Fprintf(runner.Log, "state-runner: "+format+"\n", args...)
}

// runExecution shares the live revision and cancellation flag between the
// execute path and its heartbeat goroutine.
type runExecution struct {
	mutex     sync.Mutex
	revision  int64
	cancelled bool
}

func (execution *runExecution) Revision() int64 {
	execution.mutex.Lock()
	defer execution.mutex.Unlock()
	return execution.revision
}

func (execution *runExecution) SetRevision(revision int64) {
	execution.mutex.Lock()
	defer execution.mutex.Unlock()
	execution.revision = revision
}

func (execution *runExecution) Cancelled() bool {
	execution.mutex.Lock()
	defer execution.mutex.Unlock()
	return execution.cancelled
}

func (execution *runExecution) MarkCancelled() {
	execution.mutex.Lock()
	defer execution.mutex.Unlock()
	execution.cancelled = true
}
