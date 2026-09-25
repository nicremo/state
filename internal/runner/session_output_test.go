package runner

import (
	"bytes"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/nicremo/state/internal/state"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	content, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return content
}

// The fixtures are real outputs of the three CLIs on the owner's Mac
// (25.09.2026), trimmed to the fields the runner reads.
func TestParsersReadTheProbedOutputs(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		adapter   string
		file      string
		text      string
		sessionID string
	}{
		{"claude-code", "claude_result.json", "OK", "e11a322c-c64c-41ef-990a-ee6d22767bc3"},
		{"codex", "codex_events.jsonl", "OK", "01a0d8f6-5579-72d2-817e-c2b862c4d93e"},
		{"kimi-code", "kimi_stream.jsonl", "OK", "session_af4bbe40-f9f4-4be5-a7ba-2d17f0617726"},
	} {
		parsed := parseAgentOutput(test.adapter, fixture(t, test.file))
		if parsed.Text != test.text || parsed.SessionID != test.sessionID || parsed.Failed {
			t.Errorf("%s: parsed = %+v", test.adapter, parsed)
		}
	}
}

func TestParsersKeepTheLastAnswerAndSkipNoise(t *testing.T) {
	t.Parallel()
	codex := []byte("warning: something on stdout\n" +
		`{"type":"thread.started","thread_id":"t-1"}` + "\n" +
		`{"type":"item.completed","item":{"type":"agent_message","text":"Erster Gedanke"}}` + "\n" +
		`{"type":"item.completed","item":{"type":"command_execution","text":"ls"}}` + "\n" +
		`{"type":"item.completed","item":{"type":"agent_message","text":"## Fertig\nReport liegt bereit."}}` + "\n")
	if parsed := parseAgentOutput("codex", codex); parsed.Text != "## Fertig\nReport liegt bereit." || parsed.SessionID != "t-1" {
		t.Fatalf("codex = %+v", parsed)
	}
	kimi := []byte(`{"role":"assistant","content":"Zwischenstand"}` + "\n" + `{"role":"assistant","content":"Endergebnis"}` + "\n")
	if parsed := parseAgentOutput("kimi-code", kimi); parsed.Text != "Endergebnis" {
		t.Fatalf("kimi = %+v", parsed)
	}
	claudeError := []byte(`{"type":"result","is_error":true,"result":"Failed to authenticate","session_id":"s-2"}`)
	if parsed := parseAgentOutput("claude-code", claudeError); !parsed.Failed || parsed.Text != "Failed to authenticate" {
		t.Fatalf("claude error = %+v", parsed)
	}
	if parsed := parseAgentOutput("claude-code", []byte("not json")); parsed.Text != "" || parsed.SessionID != "" {
		t.Fatalf("garbage = %+v", parsed)
	}
	if parsed := parseAgentOutput("pi-agent", []byte("plain answer\n")); parsed.Text != "plain answer" {
		t.Fatalf("plain text adapter = %+v", parsed)
	}
	unsafe := []byte(`{"type":"result","result":"x","session_id":"; rm -rf /"}`)
	if parsed := parseAgentOutput("claude-code", unsafe); parsed.SessionID != "" {
		t.Fatalf("an unsafe session ID was accepted: %+v", parsed)
	}
}

func contractWith(capabilities []string, resume string) state.TaskContract {
	return state.TaskContract{AllowedCapabilities: capabilities, ResumeSessionID: resume}
}

func TestAdapterArgumentsFollowRightsAndResume(t *testing.T) {
	t.Parallel()
	read := []string{state.CapabilityReadRepository}
	edit := []string{state.CapabilityReadRepository, state.CapabilityEditRepository}
	full := []string{state.CapabilityReadRepository, state.CapabilityEditRepository, state.CapabilityRunTests}
	for _, test := range []struct {
		adapter string
		rights  []string
		resume  string
		want    []string
	}{
		{"claude-code", read, "", []string{"-p", "--output-format", "json", "P"}},
		{"claude-code", edit, "", []string{"-p", "--output-format", "json", "--permission-mode", "acceptEdits", "P"}},
		{"claude-code", full, "s-1", []string{"-p", "--output-format", "json", "--dangerously-skip-permissions", "--resume", "s-1", "P"}},
		{"codex", read, "", []string{"exec", "--json", "--skip-git-repo-check", "-c", `sandbox_mode="read-only"`, "P"}},
		{"codex", edit, "t-1", []string{"exec", "resume", "--json", "--skip-git-repo-check", "-c", `sandbox_mode="workspace-write"`, "t-1", "P"}},
		{"codex", full, "", []string{"exec", "--json", "--skip-git-repo-check", "--dangerously-bypass-approvals-and-sandbox", "P"}},
		{"kimi-code", read, "", []string{"-p", "P", "--output-format", "stream-json"}},
		{"kimi-code", edit, "k-1", []string{"-S", "k-1", "-y", "-p", "P", "--output-format", "stream-json"}},
		{"kimi-code", full, "", []string{"--auto", "-p", "P", "--output-format", "stream-json"}},
		{"opencode", full, "", []string{"run", "P"}},
	} {
		adapter, ok := DefaultAdapters()[test.adapter].(*cliAdapter)
		if !ok {
			t.Fatalf("%s is not a CLI adapter", test.adapter)
		}
		if got := adapter.args(contractWith(test.rights, test.resume), "P"); !reflect.DeepEqual(got, test.want) {
			t.Errorf("%s %v resume %q: args = %#v, want %#v", test.adapter, test.rights, test.resume, got, test.want)
		}
	}
}

func TestRightsLevels(t *testing.T) {
	t.Parallel()
	if rightsFor(nil) != rightsRead || rightsFor([]string{state.CapabilityEditRepository}) != rightsEdit {
		t.Fatal("read or edit level wrong")
	}
	for _, capability := range []string{state.CapabilityRunTests, state.CapabilityNetworkAccess, state.CapabilityWriteState, state.CapabilityDeploy, state.CapabilityMessageExternal, state.CapabilityDestructive} {
		if rightsFor([]string{capability}) != rightsFull {
			t.Errorf("%s does not give full rights", capability)
		}
	}
}

// Review finding 2: a session ID from the CLI's output becomes an argument of
// the next round; one that looks like a flag must never be accepted.
func TestFlagShapedSessionIDsAreRejected(t *testing.T) {
	t.Parallel()
	for _, id := range []string{"--dangerously-skip-permissions", "-x", "..", ".hidden"} {
		output := []byte(`{"type":"result","result":"x","session_id":"` + id + `"}`)
		if parsed := parseAgentOutput("claude-code", output); parsed.SessionID != "" {
			t.Errorf("session ID %q was accepted", id)
		}
	}
}

// Review finding 4: output beyond the limit must not drop codex's thread ID
// from the start, and a result too large to read must fail the round
// instead of looking like an empty success.
func TestLargeOutputsKeepTheSessionOrFailTheRound(t *testing.T) {
	t.Parallel()
	var codex bytes.Buffer
	codex.WriteString(`{"type":"thread.started","thread_id":"t-keep"}` + "\n")
	noise := `{"type":"item.completed","item":{"type":"command_execution","text":"` + strings.Repeat("x", 1<<20) + `"}}` + "\n"
	for index := 0; index < 6; index++ {
		codex.WriteString(noise)
	}
	codex.WriteString(`{"type":"item.completed","item":{"type":"agent_message","text":"Fertig"}}` + "\n")
	collector := newOutputCollector("codex")
	if _, err := collector.Write(codex.Bytes()); err != nil {
		t.Fatal(err)
	}
	if parsed := collector.Finish(); parsed.SessionID != "t-keep" || parsed.Text != "Fertig" {
		t.Fatalf("codex = %+v", parsed)
	}

	huge := `{"type":"result","result":"` + strings.Repeat("y", agentOutputLimit+10) + `","session_id":"s-1"}`
	collector = newOutputCollector("claude-code")
	for start := 0; start < len(huge); start += 1 << 16 {
		end := min(start+1<<16, len(huge))
		if _, err := collector.Write([]byte(huge[start:end])); err != nil {
			t.Fatal(err)
		}
	}
	if parsed := collector.Finish(); !parsed.Failed || parsed.Text == "" {
		t.Fatalf("an unreadable result looked like success: %+v", parsed)
	}
}
