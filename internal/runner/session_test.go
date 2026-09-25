package runner

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/nicremo/state/internal/state"
)

// fakeClaude writes a `claude` on a private PATH that appends its argv to a
// log and answers like `claude -p --output-format json`. The answer counts
// the calls, so each round is recognisable.
func fakeClaude(t *testing.T, answer string) (string, string) {
	t.Helper()
	bin := t.TempDir()
	argvLog := filepath.Join(bin, "argv.log")
	script := "#!/bin/sh\n" +
		"printf '%s\\n' \"$@\" >> " + argvLog + "\n" +
		"printf -- '--\\n' >> " + argvLog + "\n" +
		"calls=$(grep -c -- '^--$' " + argvLog + ")\n" +
		answer + "\n"
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin, argvLog
}

// newSessionFixture reuses the runner fixture's server and pairing, and adds
// a Claude Code policy the runner serves.
func newSessionRunnerFixture(t *testing.T) (runnerFixture, state.ExecutionPolicy, *Runner) {
	t.Helper()
	fixture := newRunnerFixture(t, "echo unused")
	// The fixture's scheduled manual run is not part of this test.
	fixture.ownerCancelRun(t, fixture.run.ID)
	policy, err := fixture.state.CreatePolicy(context.Background(), fixture.owner, state.CreatePolicyInput{
		Name: "karla-claude", ProjectID: fixture.project.ID, Adapter: "claude-code", Mode: state.ExecutionModeSupervised,
		AllowedCapabilities: []string{state.CapabilityReadRepository, state.CapabilityEditRepository, state.CapabilityRunTests},
		TimeoutMinutes:      5, ClientRequestID: uuid.NewString(),
	})
	if err != nil {
		t.Fatal(err)
	}
	runners, err := fixture.state.ListRunners(context.Background())
	if err != nil || len(runners) != 1 {
		t.Fatalf("runners = %v, %v", runners, err)
	}
	adapters := []string{"script", "claude-code"}
	if _, err := fixture.state.UpdateRunner(context.Background(), fixture.owner, runners[0].ID, state.UpdateRunnerInput{Adapters: &adapters, ExpectedRevision: runners[0].Revision, ClientRequestID: uuid.NewString()}); err != nil {
		t.Fatal(err)
	}
	process := fixture.newRunner("echo unused", 0)
	process.Config.Adapters = adapters
	process.Adapters = DefaultAdapters()
	return fixture, policy, process
}

func TestSessionRoundsResumeTheCLISessionAndReportTheAnswer(t *testing.T) {
	// Not parallel: changes PATH.
	bin, argvLog := fakeClaude(t, `printf '{"type":"result","is_error":false,"result":"## Runde %s\\nFertig.","session_id":"sess-abc"}\n' "$calls"`)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+"/usr/bin:/bin")
	fixture, policy, process := newSessionRunnerFixture(t)
	ctx := context.Background()

	session, err := fixture.state.StartAgentSession(ctx, fixture.owner, state.StartAgentSessionInput{
		ReminderID: fixture.reminder.ID, PolicyID: policy.ID, Instruction: "Nimm die Zahlen vom September.",
		MutationMetadata: state.MutationMetadata{ClientRequestID: uuid.NewString()},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := process.Run(ctx, true); err != nil {
		t.Fatalf("round 1: %v", err)
	}
	view, _ := fixture.state.GetAgentSession(ctx, session.ID)
	first := view.Turns[0]
	if first.Status != state.AgentRunStatusSucceeded || first.ResultText != "## Runde 1\nFertig." || first.HarnessSessionID != "sess-abc" {
		t.Fatalf("round 1 = %s %q %q %q", first.Status, first.ResultText, first.HarnessSessionID, first.ResultSummary)
	}

	if _, err := fixture.state.SendAgentSessionMessage(ctx, fixture.owner, state.SendAgentSessionMessageInput{
		SessionID: session.ID, Text: "Prüf die Zahlen nochmal.", MutationMetadata: state.MutationMetadata{ClientRequestID: uuid.NewString()},
	}); err != nil {
		t.Fatal(err)
	}
	if err := process.Run(ctx, true); err != nil {
		t.Fatalf("round 2: %v", err)
	}
	view, _ = fixture.state.GetAgentSession(ctx, session.ID)
	if second := view.Turns[1]; second.Status != state.AgentRunStatusSucceeded || second.ResultText != "## Runde 2\nFertig." {
		t.Fatalf("round 2 = %s %q", second.Status, second.ResultText)
	}
	calls := strings.Split(strings.TrimSuffix(readFile(t, argvLog), "--\n"), "--\n")
	if len(calls) != 2 {
		t.Fatalf("claude calls = %d: %q", len(calls), calls)
	}
	if !strings.Contains(calls[0], "--dangerously-skip-permissions") || strings.Contains(calls[0], "--resume") || !strings.Contains(calls[0], "Nimm die Zahlen vom September.") {
		t.Fatalf("round 1 argv = %q", calls[0])
	}
	if !strings.Contains(calls[1], "--resume\nsess-abc\n") || !strings.HasSuffix(calls[1], "Prüf die Zahlen nochmal.\n") || strings.Contains(calls[1], "Context:") {
		t.Fatalf("round 2 argv = %q", calls[1])
	}
}

func TestSessionRoundFailsWhenTheCLIReportsAnError(t *testing.T) {
	// Not parallel: changes PATH.
	bin, _ := fakeClaude(t, `printf '{"type":"result","is_error":true,"result":"Failed to authenticate","session_id":"sess-x"}\n'`)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+"/usr/bin:/bin")
	fixture, policy, process := newSessionRunnerFixture(t)
	ctx := context.Background()
	session, err := fixture.state.StartAgentSession(ctx, fixture.owner, state.StartAgentSessionInput{
		ReminderID: fixture.reminder.ID, PolicyID: policy.ID, MutationMetadata: state.MutationMetadata{ClientRequestID: uuid.NewString()},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := process.Run(ctx, true); err != nil {
		t.Fatal(err)
	}
	view, _ := fixture.state.GetAgentSession(ctx, session.ID)
	if view.Status != state.SessionStatusFailed || view.Turns[0].ResultText != "Failed to authenticate" {
		t.Fatalf("session = %s, round = %+v", view.Status, view.Turns[0])
	}
}

func TestOpenOnMacWritesACommandFileAndOpensIt(t *testing.T) {
	// Not parallel: changes PATH.
	bin, argvLog := fakeClaude(t, `printf '{"type":"result","result":"Fertig","session_id":"sess-open"}\n'`)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+"/usr/bin:/bin")
	fixture, policy, process := newSessionRunnerFixture(t)
	opened := make([]string, 0)
	process.OpenTerminal = func(path string) error {
		opened = append(opened, path)
		return nil
	}
	ctx := context.Background()
	session, err := fixture.state.StartAgentSession(ctx, fixture.owner, state.StartAgentSessionInput{
		ReminderID: fixture.reminder.ID, PolicyID: policy.ID, MutationMetadata: state.MutationMetadata{ClientRequestID: uuid.NewString()},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := process.Run(ctx, true); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.state.OpenAgentSessionOnMac(ctx, fixture.owner, state.AgentSessionActionInput{SessionID: session.ID, MutationMetadata: state.MutationMetadata{ClientRequestID: uuid.NewString()}}); err != nil {
		t.Fatal(err)
	}
	if err := process.Run(ctx, true); err != nil {
		t.Fatal(err)
	}
	if len(opened) != 1 {
		t.Fatalf("opened = %v", opened)
	}
	commandFile := opened[0]
	checkout, err := filepath.EvalSymlinks(fixture.checkoutPath)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(checkout, ".state", "sessions", session.ID, "open.command"); commandFile != want {
		t.Fatalf("command file = %s, want %s", commandFile, want)
	}
	info, err := os.Stat(commandFile)
	if err != nil || info.Mode().Perm()&0o100 == 0 {
		t.Fatalf("command file not executable: %v %v", info, err)
	}
	content := readFile(t, commandFile)
	if !strings.Contains(content, "cd '"+checkout+"'") || !strings.Contains(content, "claude --resume 'sess-open'") {
		t.Fatalf("command file = %q", content)
	}
	view, _ := fixture.state.GetAgentSession(ctx, session.ID)
	last := view.Turns[len(view.Turns)-1]
	if last.TurnKind != state.TurnKindOpenTerminal || last.Status != state.AgentRunStatusSucceeded {
		t.Fatalf("open round = %+v", last)
	}
	if calls := strings.Count(readFile(t, argvLog), "--\n"); calls != 1 {
		t.Fatalf("the agent was started %d times, want only the first round", calls)
	}
}

func TestTerminalCommandQuotesPaths(t *testing.T) {
	t.Parallel()
	command, err := terminalCommand("codex", "/Users/x/it's here", "t-1")
	if err != nil || !strings.Contains(command, `cd '/Users/x/it'\''s here'`) || !strings.Contains(command, "codex resume 't-1'") {
		t.Fatalf("command = %q, %v", command, err)
	}
	if command, err := terminalCommand("kimi-code", "/p", "k-1"); err != nil || !strings.Contains(command, "kimi -S 'k-1'") {
		t.Fatalf("kimi command = %q, %v", command, err)
	}
	if _, err := terminalCommand("pi-agent", "/p", "x"); err == nil {
		t.Fatal("an adapter without resume support got a command")
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(content)
}
