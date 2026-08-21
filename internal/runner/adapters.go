package runner

import (
	"context"
	"errors"
	"fmt"
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

// Result is the terminal evidence of an adapter process.
type Result struct {
	ExitCode int
	Tail     string
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

// DefaultAdapters returns the shipped adapter registry. The test-only script
// adapter is registered only when STATE_RUNNER_TEST_ADAPTER=1, so integration
// tests never need real agent CLIs and production processes never get it.
func DefaultAdapters() map[string]Adapter {
	adapters := map[string]Adapter{
		"codex": &cliAdapter{
			slug:   "codex",
			binary: "codex",
			args:   func(prompt string) []string { return []string{"exec", "--skip-git-repo-check", prompt} },
		},
		"claude-code": &cliAdapter{
			slug:   "claude-code",
			binary: "claude",
			args:   func(prompt string) []string { return []string{"-p", prompt} },
		},
		"opencode": &cliAdapter{
			slug:   "opencode",
			binary: "opencode",
			args:   func(prompt string) []string { return []string{"run", prompt} },
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
	args   func(prompt string) []string
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
	command := exec.CommandContext(ctx, adapter.binary, adapter.args(request.Prompt)...)
	command.Dir = request.Dir
	tail := &tailBuffer{limit: OutputTailLimit}
	command.Stdout = tail
	command.Stderr = tail
	prepareProcessGroup(command)
	if err := command.Start(); err != nil {
		return nil, fmt.Errorf("start %s: %w", adapter.binary, err)
	}
	return &processSession{command: command, tail: tail}, nil
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
}

func (session *processSession) Wait(_ context.Context) (Result, error) {
	err := session.command.Wait()
	result := Result{ExitCode: 0, Tail: session.tail.String()}
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
