package runner

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nicremo/state/internal/state"
)

// fakeT3 writes a t3 stand-in that logs every call (argv, then stdin for
// thread calls) and answers like the real CLI 0.1.7. mode picks the outcome
// of the thread call: ok, waiting, failed or noproject.
func fakeT3(t *testing.T, dir string, mode string) string {
	t.Helper()
	log := filepath.Join(dir, "calls.log")
	script := `#!/bin/sh
printf 'argv:' >> ` + log + `
printf ' %s' "$@" >> ` + log + `
printf '\n' >> ` + log + `
case "$1 $2" in
"thread list")
  if [ "` + mode + `" = down ]; then exit 3; fi
  if [ "` + mode + `" = noproject ] && [ ! -f ` + filepath.Join(dir, "added") + ` ]; then
    echo "No T3 Code project matches '$5'. Pass a project id or a path inside a project." >&2; exit 1
  fi
  if [ "` + mode + `" = withthreads ]; then
    echo '[{"threadId":"old","providerInstanceId":"claudeAgent_claude_hub","model":"claude-opus-5-5"}]'
  else
    echo '[]'
  fi;;
"project add") touch ` + filepath.Join(dir, "added") + `; echo "Added";;
"thread show") echo '{"thread":{"threadId":"'"$6"'","link":"https://app.t3.codes/threads/env-1/'"$6"'"},"messages":[]}';;
"thread start"|"thread send")
  printf 'stdin:' >> ` + log + `; cat >> ` + log + `; printf '\n' >> ` + log + `
  if [ "$2" = start ]; then echo "Thread t3-thread-1" >&2; fi
  case "` + mode + `" in
  waiting) exit 2;;
  failed) echo "Thread t3-thread-1 failed: provider quota exceeded" >&2; exit 1;;
  *) printf '## Erledigt\nAlles fertig.\n';;
  esac;;
esac
`
	path := filepath.Join(dir, "t3")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return log
}

func fullRightsContract() state.TaskContract {
	contract := testContract()
	contract.Objective = "Baue den Export\nmit Details"
	contract.AllowedCapabilities = []string{state.CapabilityReadRepository, state.CapabilityEditRepository, state.CapabilityRunTests}
	contract.ContractHash = contract.ComputeHash()
	return contract
}

func runT3(t *testing.T, mode string, contract state.TaskContract) (Result, string) {
	t.Helper()
	bin := t.TempDir()
	log := fakeT3(t, bin, mode)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+"/usr/bin:/bin")
	adapter := &t3Adapter{home: t.TempDir()}
	session, err := adapter.Start(context.Background(), StartRequest{Contract: contract, Dir: t.TempDir(), Prompt: "Prompt für T3"})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	result, err := session.Wait(context.Background())
	if err != nil {
		t.Fatalf("Wait() error = %v", err)
	}
	return result, readFile(t, log)
}

func TestT3FirstRoundStartsAThreadAndReportsReplyAndLink(t *testing.T) {
	result, calls := runT3(t, "ok", fullRightsContract())
	if result.ExitCode != 0 || result.Text != "## Erledigt\nAlles fertig." {
		t.Fatalf("result = %+v", result)
	}
	if result.HarnessSessionID != "t3-thread-1" || result.HarnessLink != "https://app.t3.codes/threads/env-1/t3-thread-1" {
		t.Fatalf("session = %q link = %q", result.HarnessSessionID, result.HarnessLink)
	}
	for _, want := range []string{"argv: thread list --json --project ", "argv: thread start --wait --title Baue den Export ", "stdin:Prompt für T3", "argv: thread show --json --messages 1 t3-thread-1"} {
		if !strings.Contains(calls, want) {
			t.Fatalf("calls miss %q:\n%s", want, calls)
		}
	}
	if strings.Contains(calls, "project add") {
		t.Fatalf("known project was added again:\n%s", calls)
	}
}

func TestT3AddsTheProjectWhenT3KnowsNone(t *testing.T) {
	result, calls := runT3(t, "noproject", fullRightsContract())
	if result.ExitCode != 0 || !strings.Contains(calls, "argv: project add ") {
		t.Fatalf("result = %+v calls:\n%s", result, calls)
	}
}

func TestT3LaterRoundSendsToTheThread(t *testing.T) {
	contract := fullRightsContract()
	contract.ResumeSessionID = "t3-thread-1"
	contract.ContractHash = contract.ComputeHash()
	result, calls := runT3(t, "ok", contract)
	if !strings.Contains(calls, "argv: thread send --wait t3-thread-1 -") || strings.Contains(calls, "thread list") {
		t.Fatalf("calls:\n%s", calls)
	}
	// The send call prints no "Thread" line; the ID comes from the contract.
	if result.HarnessSessionID != "t3-thread-1" || result.HarnessLink == "" {
		t.Fatalf("result = %+v", result)
	}
}

func TestT3WaitingThreadHandsTheRoundOver(t *testing.T) {
	result, _ := runT3(t, "waiting", fullRightsContract())
	if result.ExitCode != 0 || result.Text != t3WaitingText || result.HarnessLink == "" {
		t.Fatalf("result = %+v", result)
	}
}

func TestT3FailedRunReportsT3sMessage(t *testing.T) {
	result, _ := runT3(t, "failed", fullRightsContract())
	if result.ExitCode != 1 || result.Text != "Thread t3-thread-1 failed: provider quota exceeded" {
		t.Fatalf("result = %+v", result)
	}
}

func TestT3RefusesPoliciesBelowFullRights(t *testing.T) {
	bin := t.TempDir()
	fakeT3(t, bin, "ok")
	t.Setenv("PATH", bin+string(os.PathListSeparator)+"/usr/bin:/bin")
	adapter := &t3Adapter{home: t.TempDir()}
	contract := testContract()
	contract.AllowedCapabilities = []string{state.CapabilityReadRepository, state.CapabilityEditRepository}
	err := adapter.Validate(contract)
	if !errors.Is(err, ErrAdapterUnavailable) || !strings.Contains(err.Error(), t3RightsText) {
		t.Fatalf("Validate() = %v", err)
	}
}

func TestT3FindsTheCLIInTheT3Home(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".t3", "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	fakeT3(t, filepath.Join(home, ".t3", "bin"), "ok")
	t.Setenv("PATH", "/usr/bin:/bin")
	adapter := &t3Adapter{home: home}
	if path, err := adapter.binary(); err != nil || path != filepath.Join(home, ".t3", "bin", "t3") {
		t.Fatalf("binary() = %q, %v", path, err)
	}
	adapter = &t3Adapter{home: t.TempDir()}
	if _, err := adapter.binary(); !errors.Is(err, ErrAdapterUnavailable) {
		t.Fatalf("missing CLI error = %v", err)
	}
}

func TestT3TitleUsesTheFirstLineBounded(t *testing.T) {
	t.Parallel()
	if got := t3Title("  Erste Zeile\nzweite"); got != "Erste Zeile" {
		t.Fatalf("title = %q", got)
	}
	if got := t3Title(strings.Repeat("ä", 120)); len([]rune(got)) != t3TitleLimit {
		t.Fatalf("title runes = %d", len([]rune(got)))
	}
	if got := t3Title(""); got != "State" {
		t.Fatalf("empty title = %q", got)
	}
}

func TestT3NewThreadUsesTheNewestThreadsProviderAndModel(t *testing.T) {
	result, calls := runT3(t, "withthreads", fullRightsContract())
	if result.ExitCode != 0 || !strings.Contains(calls, " --provider claudeAgent_claude_hub --model claude-opus-5-5 ") {
		t.Fatalf("result = %+v calls:\n%s", result, calls)
	}
}

func TestNewestT3ModelSkipsThreadsWithoutAModel(t *testing.T) {
	t.Parallel()
	got := newestT3Model(`[{"providerInstanceId":"","model":""},{"providerInstanceId":"codex_main","model":"gpt-6"}]`)
	if got.provider != "codex_main" || got.model != "gpt-6" {
		t.Fatalf("model = %+v", got)
	}
	if got := newestT3Model("not json"); got != (t3Model{}) {
		t.Fatalf("garbage = %+v", got)
	}
}

func TestT3UnreachableNamesTheCause(t *testing.T) {
	bin := t.TempDir()
	fakeT3(t, bin, "down")
	t.Setenv("PATH", bin+string(os.PathListSeparator)+"/usr/bin:/bin")
	adapter := &t3Adapter{home: t.TempDir()}
	_, err := adapter.Start(context.Background(), StartRequest{Contract: fullRightsContract(), Dir: t.TempDir(), Prompt: "x"})
	if !errors.Is(err, ErrAdapterUnavailable) || !strings.Contains(err.Error(), "exit status 3") {
		t.Fatalf("Start() error = %v", err)
	}
}

func TestUnavailableTextDropsInternalPrefixes(t *testing.T) {
	t.Parallel()
	got := unavailableText("start adapter: adapter_unavailable: T3 Code did not answer (exit status 3), is the app running?")
	if got != "T3 Code did not answer (exit status 3), is the app running?" {
		t.Fatalf("text = %q", got)
	}
}
