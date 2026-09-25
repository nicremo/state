package runner

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/google/uuid"

	"github.com/nicremo/state/internal/state"
)

// terminalCommand is the shell script that resumes an agent session in a
// terminal: into the project folder, then the CLI's interactive resume. The
// owner sits at the terminal now, so the CLI may ask as usual.
func terminalCommand(adapter string, checkoutDir string, sessionID string) (string, error) {
	if !state.ValidHarnessSessionID(sessionID) {
		return "", errors.New("invalid agent session ID")
	}
	var resume string
	switch adapter {
	case "claude-code":
		resume = "claude --resume " + shellQuote(sessionID)
	case "codex":
		resume = "codex resume " + shellQuote(sessionID)
	case "kimi-code":
		resume = "kimi -S " + shellQuote(sessionID)
	default:
		return "", fmt.Errorf("adapter %q cannot resume a session in a terminal", adapter)
	}
	return "#!/bin/zsh -l\n" +
		"# Opened by State: continue this agent session here.\n" +
		"cd " + shellQuote(checkoutDir) + " || exit 1\n" +
		"exec " + resume + "\n", nil
}

// shellQuote wraps a value in single quotes for a POSIX shell.
func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}

// openOnMac executes an open_terminal round: it writes the command file into
// the project's .state folder and opens it, which starts a terminal on this
// Mac. The agent itself is not started by the runner.
func (runner *Runner) openOnMac(ctx context.Context, run state.AgentRun, revision int64, checkoutDir string) error {
	command, err := terminalCommand(run.Adapter, checkoutDir, run.TaskContract.ResumeSessionID)
	if err != nil {
		return runner.failUnavailable(ctx, run, revision, err.Error())
	}
	directory := filepath.Join(checkoutDir, ".state", "sessions", run.TaskContract.SessionID)
	if !state.ValidHarnessSessionID(run.TaskContract.SessionID) {
		return runner.failUnavailable(ctx, run, revision, "invalid session ID")
	}
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return runner.failUnavailable(ctx, run, revision, fmt.Sprintf("create session folder: %v", err))
	}
	path := filepath.Join(directory, "open.command")
	if err := os.WriteFile(path, []byte(command), 0o700); err != nil {
		return runner.failUnavailable(ctx, run, revision, fmt.Sprintf("write command file: %v", err))
	}
	if err := os.Chmod(path, 0o700); err != nil {
		return runner.failUnavailable(ctx, run, revision, fmt.Sprintf("make command file executable: %v", err))
	}
	open := runner.OpenTerminal
	if open == nil {
		open = func(path string) error { return exec.Command("/usr/bin/open", path).Run() }
	}
	if err := open(path); err != nil {
		return runner.failUnavailable(ctx, run, revision, fmt.Sprintf("open terminal: %v", err))
	}
	text := "Opened in a terminal on " + runner.Config.Name + "."
	_, err = runner.Client.Complete(ctx, state.CompleteRunInput{
		RunID:            run.ID,
		Outcome:          state.AgentRunStatusSucceeded,
		ResultSummary:    text,
		ResultText:       text,
		ExitCode:         0,
		ExpectedRevision: revision,
		MutationMetadata: state.MutationMetadata{ClientRequestID: uuid.NewString(), Source: "state-runner"},
	})
	if err != nil && !errors.Is(err, state.ErrRunStateConflict) {
		return fmt.Errorf("complete open round %s: %w", run.ID, err)
	}
	runner.note("run %s opened session %s in a terminal", run.ID, run.TaskContract.SessionID)
	return nil
}
