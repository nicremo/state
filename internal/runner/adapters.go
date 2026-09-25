package runner

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"

	"github.com/nicremo/state/internal/state"
)

// ErrAdapterUnavailable reports a missing or unusable local harness binary.
var ErrAdapterUnavailable = errors.New("adapter_unavailable")

// OutputTailLimit bounds the combined adapter output the runner keeps. The
// tail never leaves the workstation except as a redacted one-line summary.
const OutputTailLimit = 64 << 10

// StartRequest carries everything an adapter needs to launch one run.
type StartRequest struct {
	Contract state.TaskContract
	Dir      string
	Prompt   string
}

// Result is the terminal evidence of an adapter process. Text and
// HarnessSessionID come from CLIs with structured output; AgentFailed is set
// when the CLI reported the round as failed although it exited cleanly.
type Result struct {
	ExitCode         int
	Tail             string
	Text             string
	HarnessSessionID string
	AgentFailed      bool
}

// Session is one launched adapter process.
type Session interface {
	Wait(ctx context.Context) (Result, error)
	Cancel(ctx context.Context) error
}

// Adapter maps a validated task contract to a local harness invocation.
// Adapter-owned flags, prompts and output capture stay local; only the typed
// contract in and the exit evidence out cross the boundary.
type Adapter interface {
	Name() string
	Validate(contract state.TaskContract) error
	Start(ctx context.Context, request StartRequest) (Session, error)
}

// DefaultAdapters returns the shipped adapter registry: codex, claude-code,
// kimi-code, opencode, pi-agent (Pi Agent) and deepseek-harness (DeepSeek
// Harness). The
// test-only script adapter is registered only when
// STATE_RUNNER_TEST_ADAPTER=1, so integration tests never need real agent CLIs
// and production processes never get it.
func DefaultAdapters() map[string]Adapter {
	adapters := map[string]Adapter{
		"codex": &cliAdapter{
			slug:   "codex",
			binary: "codex",
			args:   codexArguments,
		},
		"claude-code": &cliAdapter{
			slug:   "claude-code",
			binary: "claude",
			args:   claudeArguments,
		},
		"kimi-code": &cliAdapter{
			slug:   "kimi-code",
			binary: "kimi",
			args:   kimiArguments,
		},
		"opencode": &cliAdapter{
			slug:   "opencode",
			binary: "opencode",
			args:   func(_ state.TaskContract, prompt string) []string { return []string{"run", prompt} },
		},
		"pi-agent": &cliAdapter{
			slug:   "pi-agent",
			binary: "pi",
			args:   func(_ state.TaskContract, prompt string) []string { return []string{"-p", prompt} },
		},
		"deepseek-harness": &cliAdapter{
			slug:   "deepseek-harness",
			binary: "dsh",
			args:   func(_ state.TaskContract, prompt string) []string { return []string{"--profile", "headless", prompt} },
		},
	}
	if os.Getenv("STATE_RUNNER_TEST_ADAPTER") == "1" {
		script := os.Getenv("STATE_RUNNER_TEST_SCRIPT")
		if script == "" {
			script = "echo state-runner test adapter"
		}
		adapters["script"] = &scriptAdapter{script: script}
	}
	return adapters
}

// BuildPrompt renders the single prompt argv element for a run. The prompt
// points at the local context and contract files instead of embedding them.
func BuildPrompt(contract state.TaskContract) string {
	// A later round of a session continues a conversation the agent already
	// knows; it gets the owner's message only.
	if contract.ResumeSessionID != "" {
		return contract.Objective
	}
	var builder strings.Builder
	builder.WriteString(contract.Objective)
	if len(contract.AcceptanceCriteria) > 0 {
		builder.WriteString("\n\nAcceptance criteria:\n")
		for _, criterion := range contract.AcceptanceCriteria {
			fmt.Fprintf(&builder, "- %s\n", criterion)
		}
	}
	fmt.Fprintf(&builder, "\nContext: see .state/context/current.md and .state/runs/%s/contract.json", contract.RunID)
	return builder.String()
}

// cliAdapter launches a harness CLI non-interactively. The prompt is passed as
// exactly one argv element; no shell is involved.
type cliAdapter struct {
	slug   string
	binary string
	args   func(contract state.TaskContract, prompt string) []string
}

func (adapter *cliAdapter) Name() string {
	return adapter.slug
}

func (adapter *cliAdapter) Validate(state.TaskContract) error {
	if _, err := exec.LookPath(adapter.binary); err != nil {
		return fmt.Errorf("%w: %s not found on PATH", ErrAdapterUnavailable, adapter.binary)
	}
	return nil
}

func (adapter *cliAdapter) Start(ctx context.Context, request StartRequest) (Session, error) {
	if err := adapter.Validate(request.Contract); err != nil {
		return nil, err
	}
	command := exec.CommandContext(ctx, adapter.binary, adapter.args(request.Contract, request.Prompt)...)
	command.Dir = request.Dir
	tail := &tailBuffer{limit: OutputTailLimit}
	output := newOutputCollector(adapter.slug)
	command.Stdout = io.MultiWriter(tail, output)
	command.Stderr = tail
	prepareProcessGroup(command)
	if err := command.Start(); err != nil {
		return nil, fmt.Errorf("start %s: %w", adapter.binary, err)
	}
	return &processSession{command: command, tail: tail, output: output}, nil
}

// scriptAdapter is the test-only adapter: it runs one fixed local script
// through /bin/sh so integration tests exercise the full loop without a real
// agent CLI.
type scriptAdapter struct {
	script string
}

func (adapter *scriptAdapter) Name() string {
	return "script"
}

func (adapter *scriptAdapter) Validate(state.TaskContract) error {
	if _, err := exec.LookPath("sh"); err != nil {
		return fmt.Errorf("%w: sh not found on PATH", ErrAdapterUnavailable)
	}
	return nil
}

func (adapter *scriptAdapter) Start(ctx context.Context, request StartRequest) (Session, error) {
	if err := adapter.Validate(request.Contract); err != nil {
		return nil, err
	}
	command := exec.CommandContext(ctx, "sh", "-c", adapter.script)
	command.Dir = request.Dir
	tail := &tailBuffer{limit: OutputTailLimit}
	command.Stdout = tail
	command.Stderr = tail
	prepareProcessGroup(command)
	if err := command.Start(); err != nil {
		return nil, fmt.Errorf("start script adapter: %w", err)
	}
	return &processSession{command: command, tail: tail}, nil
}

// processSession is a running adapter process with bounded combined output.
type processSession struct {
	command *exec.Cmd
	tail    *tailBuffer
	// output reads the standard output alone; nil for the test script
	// adapter.
	output *outputCollector
}

func (session *processSession) Wait(_ context.Context) (Result, error) {
	err := session.command.Wait()
	result := Result{ExitCode: 0, Tail: session.tail.String()}
	if session.output != nil {
		parsed := session.output.Finish()
		result.Text, result.HarnessSessionID, result.AgentFailed = parsed.Text, parsed.SessionID, parsed.Failed
	}
	if err == nil {
		return result, nil
	}
	var exitError *exec.ExitError
	if errors.As(err, &exitError) {
		result.ExitCode = exitError.ExitCode()
		return result, nil
	}
	// The process was killed by signal (cancellation) or never ran.
	result.ExitCode = -1
	return result, nil
}

func (session *processSession) Cancel(_ context.Context) error {
	return killProcessGroup(session.command)
}

// tailBuffer keeps only the last `limit` bytes of a stream. It is safe for
// concurrent use by the adapter process (stdout/stderr) and status writers.
type tailBuffer struct {
	mutex sync.Mutex
	limit int
	data  []byte
}

func (buffer *tailBuffer) Write(chunk []byte) (int, error) {
	buffer.mutex.Lock()
	defer buffer.mutex.Unlock()
	buffer.data = append(buffer.data, chunk...)
	if len(buffer.data) > buffer.limit {
		buffer.data = append([]byte(nil), buffer.data[len(buffer.data)-buffer.limit:]...)
	}
	return len(chunk), nil
}

func (buffer *tailBuffer) String() string {
	buffer.mutex.Lock()
	defer buffer.mutex.Unlock()
	return string(buffer.data)
}

// rights is how much an agent may do without asking, from the policy's
// capabilities. A CLI started without a terminal cannot ask, so anything
// the policy does not allow is refused by the CLI itself.
type rights int

const (
	rightsRead rights = iota
	rightsEdit
	rightsFull
)

func rightsFor(capabilities []string) rights {
	level := rightsRead
	for _, capability := range capabilities {
		switch capability {
		case state.CapabilityRunTests, state.CapabilityNetworkAccess, state.CapabilityWriteState,
			state.CapabilityDeploy, state.CapabilityMessageExternal, state.CapabilityDestructive:
			return rightsFull
		case state.CapabilityEditRepository:
			level = rightsEdit
		}
	}
	return level
}

// claudeArguments: JSON output for the final message and the session ID,
// the permission mode from the rights, --resume for a later round.
func claudeArguments(contract state.TaskContract, prompt string) []string {
	args := []string{"-p", "--output-format", "json"}
	switch rightsFor(contract.AllowedCapabilities) {
	case rightsEdit:
		args = append(args, "--permission-mode", "acceptEdits")
	case rightsFull:
		args = append(args, "--dangerously-skip-permissions")
	}
	if contract.ResumeSessionID != "" {
		args = append(args, "--resume", contract.ResumeSessionID)
	}
	return append(args, prompt)
}

// codexArguments: JSON events, the sandbox from the rights, `exec resume`
// for a later round. The sandbox is set through -c because `exec resume`
// has no --sandbox flag.
func codexArguments(contract state.TaskContract, prompt string) []string {
	args := []string{"exec"}
	if contract.ResumeSessionID != "" {
		args = append(args, "resume")
	}
	args = append(args, "--json", "--skip-git-repo-check")
	switch rightsFor(contract.AllowedCapabilities) {
	case rightsRead:
		args = append(args, "-c", `sandbox_mode="read-only"`)
	case rightsEdit:
		args = append(args, "-c", `sandbox_mode="workspace-write"`)
	case rightsFull:
		args = append(args, "--dangerously-bypass-approvals-and-sandbox")
	}
	if contract.ResumeSessionID != "" {
		args = append(args, contract.ResumeSessionID)
	}
	return append(args, prompt)
}

// kimiArguments: stream JSON, -S for a later round, -y (routine edits and
// commands) or --auto (everything) from the rights.
func kimiArguments(contract state.TaskContract, prompt string) []string {
	args := []string{}
	if contract.ResumeSessionID != "" {
		args = append(args, "-S", contract.ResumeSessionID)
	}
	switch rightsFor(contract.AllowedCapabilities) {
	case rightsEdit:
		args = append(args, "-y")
	case rightsFull:
		args = append(args, "--auto")
	}
	return append(args, "-p", prompt, "--output-format", "stream-json")
}
