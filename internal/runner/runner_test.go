package runner

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/pocketbase/pocketbase"

	"github.com/nicremo/state/internal/api"
	stateauth "github.com/nicremo/state/internal/auth"
	"github.com/nicremo/state/internal/state"
	"github.com/nicremo/state/internal/store"
)

// runnerFixture is a booted State server (REST) with an owner, one project,
// one policy, one scheduled reminder and one claimed-able manual run, plus a
// paired runner credential — the whole loop over real HTTP.
type runnerFixture struct {
	server       *httptest.Server
	state        *state.Service
	repository   *store.PocketBaseRepository
	auth         *stateauth.Manager
	owner        state.Actor
	ownerToken   string
	project      state.Project
	policy       state.ExecutionPolicy
	reminder     state.Reminder
	run          state.AgentRun
	runnerToken  string
	workRoot     string
	checkoutPath string
}

func newRunnerFixture(t *testing.T, script string) runnerFixture {
	t.Helper()

	app := pocketbase.NewWithConfig(pocketbase.Config{
		DefaultDataDir:   t.TempDir(),
		HideStartBanner:  true,
		DataMaxOpenConns: 1,
		DataMaxIdleConns: 1,
		AuxMaxOpenConns:  1,
		AuxMaxIdleConns:  1,
	})
	if err := app.Bootstrap(); err != nil {
		t.Fatalf("Bootstrap() error = %v", err)
	}
	t.Cleanup(func() {
		if err := app.ResetBootstrapState(); err != nil {
			t.Errorf("ResetBootstrapState() error = %v", err)
		}
	})
	seed := make([]byte, ed25519.SeedSize)
	for index := range seed {
		seed[index] = byte(index + 55)
	}
	repository, err := store.NewPocketBaseRepository(app, ed25519.NewKeyFromSeed(seed))
	if err != nil {
		t.Fatalf("NewPocketBaseRepository() error = %v", err)
	}
	authManager, err := stateauth.NewManager(app, "bootstrap-secret")
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	ownerCredential, err := authManager.BootstrapOwner(context.Background(), "bootstrap-secret", stateauth.OwnerBootstrapRequest{DisplayName: "Fabian", DeviceName: "iPhone"})
	if err != nil {
		t.Fatalf("BootstrapOwner() error = %v", err)
	}
	owner, err := authManager.Authenticate(context.Background(), ownerCredential.Token)
	if err != nil {
		t.Fatalf("Authenticate() error = %v", err)
	}
	service := state.NewService(repository)
	handler := api.NewHandler(api.Config{Auth: authManager, State: service, Version: "test-version"})
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	project, err := service.CreateProject(context.Background(), owner, state.CreateProjectInput{
		Name:            "customer-api",
		RootPathHint:    "~/src/customer-api",
		ClientRequestID: uuid.NewString(),
	})
	if err != nil {
		t.Fatalf("CreateProject() error = %v", err)
	}
	policy, err := service.CreatePolicy(context.Background(), owner, state.CreatePolicyInput{
		Name:                        "nightly-review",
		ProjectID:                   project.ID,
		Adapter:                     "script",
		Mode:                        state.ExecutionModeSupervised,
		AllowedCapabilities:         []string{state.CapabilityReadRepository, state.CapabilityRunTests},
		MarkOccurrenceDoneOnSuccess: true,
		TimeoutMinutes:              1,
		ClientRequestID:             uuid.NewString(),
	})
	if err != nil {
		t.Fatalf("CreatePolicy() error = %v", err)
	}
	yesterday := time.Now().UTC().Add(-26 * time.Hour)
	reminder, err := service.CreateReminder(context.Background(), owner, state.CreateReminderInput{
		Title:           "Review the nightly metrics",
		Description:     "All checks must pass",
		ClientRequestID: uuid.NewString(),
		Schedule: &state.Schedule{
			LocalDate: yesterday.Format("2006-01-02"),
			LocalTime: yesterday.Format("15:04"),
			TimeZone:  "UTC",
			Mode:      state.TimeZoneModeFixed,
		},
	})
	if err != nil {
		t.Fatalf("CreateReminder() error = %v", err)
	}
	run, err := service.CreateManualRun(context.Background(), owner, state.CreateManualRunInput{
		ReminderID:       reminder.ID,
		PolicyID:         policy.ID,
		MutationMetadata: state.MutationMetadata{ClientRequestID: uuid.NewString()},
	})
	if err != nil {
		t.Fatalf("CreateManualRun() error = %v", err)
	}

	// Pair the runner through the same HTTP path the binary uses.
	pairing, err := authManager.CreatePairingCode(context.Background(), owner, stateauth.PairingCodeRequest{
		Kind:        state.ActorKindRunner,
		DisplayName: "Mac mini",
	})
	if err != nil {
		t.Fatalf("CreatePairingCode(runner) error = %v", err)
	}
	client := NewClient(server.URL, "", server.Client())
	credential, err := client.ExchangePairingCode(context.Background(), pairing.Code)
	if err != nil {
		t.Fatalf("ExchangePairingCode() error = %v", err)
	}
	if credential.Actor.Kind != state.ActorKindRunner {
		t.Fatalf("paired kind = %q", credential.Actor.Kind)
	}
	client = NewClient(server.URL, credential.Token, server.Client())
	if _, err := client.Register(context.Background(), state.RegisterRunnerInput{
		DisplayName:     "Mac mini",
		Projects:        []string{project.ID},
		Adapters:        []string{"script"},
		ClientRequestID: uuid.NewString(),
	}); err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	workRoot := t.TempDir()
	checkoutPath := filepath.Join(workRoot, project.Name)
	if err := os.MkdirAll(checkoutPath, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	return runnerFixture{
		server:       server,
		state:        service,
		repository:   repository,
		auth:         authManager,
		owner:        owner,
		ownerToken:   ownerCredential.Token,
		project:      project,
		policy:       policy,
		reminder:     reminder,
		run:          run,
		runnerToken:  credential.Token,
		workRoot:     workRoot,
		checkoutPath: checkoutPath,
	}
}

func (fixture runnerFixture) newRunner(script string, heartbeat time.Duration) *Runner {
	return &Runner{
		Config: RunnerConfig{
			Version:             1,
			ServerURL:           fixture.server.URL,
			Name:                "mac-mini",
			Projects:            []string{fixture.project.ID},
			Adapters:            []string{"script"},
			WorkRoot:            fixture.workRoot,
			PollIntervalSeconds: 1,
			LongPollSeconds:     1,
		},
		Client:            NewClient(fixture.server.URL, fixture.runnerToken, fixture.server.Client()),
		Adapters:          map[string]Adapter{"script": &scriptAdapter{script: script}},
		HeartbeatInterval: heartbeat,
		Log:               io.Discard,
	}
}

// ownerGetRun reads a run through the owner REST surface.
func (fixture runnerFixture) ownerGetRun(t *testing.T, runID string) state.AgentRun {
	t.Helper()
	request, err := http.NewRequestWithContext(context.Background(), http.MethodGet, fixture.server.URL+"/api/v1/runs/"+runID, nil)
	if err != nil {
		t.Fatalf("build request error = %v", err)
	}
	request.Header.Set("Authorization", "Bearer "+fixture.ownerToken)
	response, err := fixture.server.Client().Do(request)
	if err != nil {
		t.Fatalf("get run error = %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("get run status = %d", response.StatusCode)
	}
	var run state.AgentRun
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&run); err != nil {
		t.Fatalf("decode run error = %v", err)
	}
	return run
}

// ownerCancelRun cancels a run as the owner, retrying on revision races with
// the runner's heartbeats.
func (fixture runnerFixture) ownerCancelRun(t *testing.T, runID string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		run := fixture.ownerGetRun(t, runID)
		if run.Terminal() {
			return
		}
		body, err := json.Marshal(state.CancelRunInput{
			ExpectedRevision: run.Revision,
			MutationMetadata: state.MutationMetadata{ClientRequestID: uuid.NewString()},
		})
		if err != nil {
			t.Fatalf("marshal cancel error = %v", err)
		}
		request, err := http.NewRequestWithContext(context.Background(), http.MethodPost, fixture.server.URL+"/api/v1/runs/"+runID+"/cancel", bytes.NewReader(body))
		if err != nil {
			t.Fatalf("build cancel error = %v", err)
		}
		request.Header.Set("Authorization", "Bearer "+fixture.ownerToken)
		request.Header.Set("Content-Type", "application/json")
		response, err := fixture.server.Client().Do(request)
		if err != nil {
			t.Fatalf("cancel run error = %v", err)
		}
		response.Body.Close()
		if response.StatusCode == http.StatusOK {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("cancel run kept failing with status %d", response.StatusCode)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func TestRunnerFullLoopCompletesManualRun(t *testing.T) {
	t.Parallel()

	fixture := newRunnerFixture(t, "echo done")
	process := fixture.newRunner("echo done", 50*time.Millisecond)
	if err := process.Run(context.Background(), true); err != nil {
		t.Fatalf("Run(--once) error = %v", err)
	}

	finished := fixture.ownerGetRun(t, fixture.run.ID)
	if finished.Status != state.AgentRunStatusSucceeded {
		t.Fatalf("run status = %q, want succeeded", finished.Status)
	}
	if !strings.Contains(finished.ResultSummary, "exit 0:") || !strings.Contains(finished.ResultSummary, "done") {
		t.Fatalf("result summary = %q", finished.ResultSummary)
	}

	contract, err := os.ReadFile(filepath.Join(fixture.checkoutPath, ".state", "runs", fixture.run.ID, "contract.json"))
	if err != nil {
		t.Fatalf("read contract.json error = %v", err)
	}
	var writtenContract state.TaskContract
	if err := json.Unmarshal(contract, &writtenContract); err != nil {
		t.Fatalf("decode contract.json error = %v", err)
	}
	if writtenContract.RunID != fixture.run.ID || writtenContract.ContractHash != fixture.run.TaskContract.ContractHash {
		t.Fatalf("contract.json = %#v", writtenContract)
	}
	statusContents, err := os.ReadFile(filepath.Join(fixture.checkoutPath, ".state", "runs", fixture.run.ID, "status.json"))
	if err != nil {
		t.Fatalf("read status.json error = %v", err)
	}
	var status runStatusFile
	if err := json.Unmarshal(statusContents, &status); err != nil {
		t.Fatalf("decode status.json error = %v", err)
	}
	if status.Status != "succeeded" || status.ExitCode == nil || *status.ExitCode != 0 {
		t.Fatalf("status.json = %#v", status)
	}
	contextContents, err := os.ReadFile(filepath.Join(fixture.checkoutPath, ".state", "context", "current.md"))
	if err != nil || !strings.Contains(string(contextContents), "Review the nightly metrics") {
		t.Fatalf("current.md = %v, %v", contextContents, err)
	}

	// A manual run has no occurrence, so the completion rule must not touch
	// the pending scheduled occurrence.
	occurrences, err := fixture.state.ListOccurrences(context.Background(), fixture.reminder.ID, state.OccurrenceListOptions{})
	if err != nil || len(occurrences) != 1 {
		t.Fatalf("ListOccurrences() = %#v, %v", occurrences, err)
	}
	if occurrences[0].Status != state.OccurrenceStatusPending {
		t.Fatalf("occurrence status = %q, want pending", occurrences[0].Status)
	}
	if err := fixture.repository.VerifyAuditChain(context.Background()); err != nil {
		t.Fatalf("VerifyAuditChain() error = %v", err)
	}
}

func TestRunnerRejectsTamperedContract(t *testing.T) {
	t.Parallel()

	fixture := newRunnerFixture(t, "echo done")
	client := NewClient(fixture.server.URL, fixture.runnerToken, fixture.server.Client())
	claimed, err := client.Claim(context.Background(), 0)
	if err != nil {
		t.Fatalf("Claim() error = %v", err)
	}
	tampered := claimed
	tampered.TaskContract.Objective = "forged objective"

	process := fixture.newRunner("echo done", 50*time.Millisecond)
	if err := process.execute(context.Background(), tampered); err != nil {
		t.Fatalf("execute() error = %v", err)
	}
	finished := fixture.ownerGetRun(t, fixture.run.ID)
	if finished.Status != state.AgentRunStatusFailed {
		t.Fatalf("run status = %q, want failed", finished.Status)
	}
	if !strings.Contains(finished.ResultSummary, "hash") {
		t.Fatalf("result summary = %q, want hash rejection", finished.ResultSummary)
	}
	if _, err := os.Stat(filepath.Join(fixture.checkoutPath, ".state", "runs", fixture.run.ID)); !os.IsNotExist(err) {
		t.Fatal("rejected run touched the checkout")
	}
	if err := fixture.repository.VerifyAuditChain(context.Background()); err != nil {
		t.Fatalf("VerifyAuditChain() error = %v", err)
	}
}

func TestRunnerRejectsWorkdirEscape(t *testing.T) {
	t.Parallel()

	fixture := newRunnerFixture(t, "echo done")
	outside := t.TempDir()
	if err := os.Remove(fixture.checkoutPath); err != nil {
		t.Fatalf("Remove() error = %v", err)
	}
	if err := os.Symlink(outside, fixture.checkoutPath); err != nil {
		t.Fatalf("Symlink() error = %v", err)
	}

	process := fixture.newRunner("echo done", 50*time.Millisecond)
	if err := process.Run(context.Background(), true); err != nil {
		t.Fatalf("Run(--once) error = %v", err)
	}
	finished := fixture.ownerGetRun(t, fixture.run.ID)
	if finished.Status != state.AgentRunStatusFailed {
		t.Fatalf("run status = %q, want failed", finished.Status)
	}
	if !strings.Contains(finished.ResultSummary, "escapes work root") {
		t.Fatalf("result summary = %q, want workdir escape rejection", finished.ResultSummary)
	}
}

func TestRunnerReportsMissingAdapterBinary(t *testing.T) {
	t.Parallel()

	fixture := newRunnerFixture(t, "echo done")
	process := fixture.newRunner("echo done", 50*time.Millisecond)
	process.Adapters = map[string]Adapter{
		"script": &cliAdapter{slug: "script", binary: "state-runner-definitely-missing-binary", args: func(prompt string) []string { return []string{prompt} }},
	}
	if err := process.Run(context.Background(), true); err != nil {
		t.Fatalf("Run(--once) error = %v", err)
	}
	finished := fixture.ownerGetRun(t, fixture.run.ID)
	if finished.Status != state.AgentRunStatusFailed {
		t.Fatalf("run status = %q, want failed", finished.Status)
	}
	if !strings.Contains(finished.ResultSummary, "adapter_unavailable") {
		t.Fatalf("result summary = %q, want adapter_unavailable", finished.ResultSummary)
	}
}

func TestRunnerHeartbeatExtendsLease(t *testing.T) {
	t.Parallel()

	fixture := newRunnerFixture(t, "sleep 1")
	process := fixture.newRunner("sleep 1", 50*time.Millisecond)
	done := make(chan error, 1)
	go func() {
		done <- process.Run(context.Background(), true)
	}()

	// The lease must grow between the first observation (claim or later) and a
	// heartbeat: each heartbeat re-arms lease_expires_at from its own clock.
	baseline := time.Time{}
	extended := false
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		run := fixture.ownerGetRun(t, fixture.run.ID)
		if run.Terminal() {
			break
		}
		if run.LeaseExpiresAt != nil {
			if baseline.IsZero() {
				baseline = *run.LeaseExpiresAt
			} else if run.LeaseExpiresAt.After(baseline) {
				extended = true
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	if baseline.IsZero() || !extended {
		t.Fatalf("never observed lease extension (baseline %v)", baseline)
	}
	if err := <-done; err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if finished := fixture.ownerGetRun(t, fixture.run.ID); finished.Status != state.AgentRunStatusSucceeded {
		t.Fatalf("run status = %q, want succeeded", finished.Status)
	}
}

func TestRunnerKillsAdapterOnOwnerCancellation(t *testing.T) {
	t.Parallel()

	fixture := newRunnerFixture(t, "sleep 30")
	process := fixture.newRunner("sleep 30", 50*time.Millisecond)
	done := make(chan error, 1)
	started := time.Now()
	go func() {
		done <- process.Run(context.Background(), true)
	}()

	deadline := time.Now().Add(15 * time.Second)
	for {
		run := fixture.ownerGetRun(t, fixture.run.ID)
		if run.Status == state.AgentRunStatusRunning {
			break
		}
		if run.Terminal() || time.Now().After(deadline) {
			t.Fatalf("run never reached running (status %q)", run.Status)
		}
		time.Sleep(20 * time.Millisecond)
	}
	fixture.ownerCancelRun(t, fixture.run.ID)

	if err := <-done; err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if elapsed := time.Since(started); elapsed > 15*time.Second {
		t.Fatalf("cancelled adapter kept running for %v", elapsed)
	}
	finished := fixture.ownerGetRun(t, fixture.run.ID)
	if finished.Status != state.AgentRunStatusCancelled {
		t.Fatalf("run status = %q, want cancelled", finished.Status)
	}
	if err := fixture.repository.VerifyAuditChain(context.Background()); err != nil {
		t.Fatalf("VerifyAuditChain() error = %v", err)
	}
}

func TestRunnerOnceWithoutWorkExitsCleanly(t *testing.T) {
	t.Parallel()

	fixture := newRunnerFixture(t, "echo done")
	process := fixture.newRunner("echo done", 50*time.Millisecond)
	// Claim the only run so the loop finds nothing.
	client := NewClient(fixture.server.URL, fixture.runnerToken, fixture.server.Client())
	if _, err := client.Claim(context.Background(), 0); err != nil {
		t.Fatalf("Claim() error = %v", err)
	}
	if err := process.Run(context.Background(), true); err != nil {
		t.Fatalf("Run(--once) with no work error = %v", err)
	}
}

func TestSummarizeResultRedactsSecrets(t *testing.T) {
	t.Parallel()

	summary := summarizeResult(Result{ExitCode: 0, Tail: "token: abc123\nall green"})
	if strings.Contains(summary, "abc123") || !strings.Contains(summary, "all green") || !strings.Contains(summary, "exit 0:") {
		t.Fatalf("summary = %q", summary)
	}
	if got := summarizeResult(Result{ExitCode: 1, Tail: strings.Repeat("x", 3000)}); len([]rune(got)) > maxResultSummaryLength {
		t.Fatalf("summary length = %d, want <= %d", len([]rune(got)), maxResultSummaryLength)
	}
}
