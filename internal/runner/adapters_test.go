package runner

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/nicremo/state/internal/state"
)

func testContract() state.TaskContract {
	contract := state.TaskContract{
		RunID:               "01989f4a-ddfa-73a5-a131-3a6ef6a09cba",
		CorrelationID:       "01989f4a-ddfa-73a5-a131-3a6ef6a09cba",
		Objective:           "Review the nightly metrics",
		AcceptanceCriteria:  []string{"All checks must pass"},
		ProjectID:           "01989f4a-ddfa-769f-bd09-53052672c44f",
		ProjectName:         "customer-api",
		PolicyID:            "01989f4a-ddfa-7c42-9e7d-0a2f4bb2f2a2",
		PolicyRevision:      1,
		AllowedCapabilities: []string{state.CapabilityReadRepository},
		TimeoutMinutes:      30,
	}
	contract.ContractHash = contract.ComputeHash()
	return contract
}

func TestBuildPromptCarriesObjectiveCriteriaAndContextPointer(t *testing.T) {
	t.Parallel()

	prompt := BuildPrompt(testContract())
	for _, want := range []string{"Review the nightly metrics", "- All checks must pass", ".state/context/current.md", ".state/runs/01989f4a-ddfa-73a5-a131-3a6ef6a09cba/contract.json"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt misses %q:\n%s", want, prompt)
		}
	}
}

func TestScriptAdapterRunsAndCapturesTail(t *testing.T) {
	t.Parallel()

	adapter := &scriptAdapter{script: "echo hello-from-script"}
	session, err := adapter.Start(context.Background(), StartRequest{Contract: testContract(), Dir: t.TempDir(), Prompt: "p"})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	result, err := session.Wait(context.Background())
	if err != nil || result.ExitCode != 0 {
		t.Fatalf("Wait() = %#v, %v", result, err)
	}
	if !strings.Contains(result.Tail, "hello-from-script") {
		t.Fatalf("tail = %q", result.Tail)
	}
}

func TestScriptAdapterPropagatesExitCode(t *testing.T) {
	t.Parallel()

	adapter := &scriptAdapter{script: "echo oops; exit 3"}
	session, err := adapter.Start(context.Background(), StartRequest{Contract: testContract(), Dir: t.TempDir(), Prompt: "p"})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	result, _ := session.Wait(context.Background())
	if result.ExitCode != 3 || !strings.Contains(result.Tail, "oops") {
		t.Fatalf("result = %#v", result)
	}
}

func TestScriptAdapterBoundsOutputTail(t *testing.T) {
	t.Parallel()

	adapter := &scriptAdapter{script: "i=0; while [ $i -lt 3000 ]; do echo 0123456789012345678901234; i=$((i+1)); done"}
	session, err := adapter.Start(context.Background(), StartRequest{Contract: testContract(), Dir: t.TempDir(), Prompt: "p"})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	result, _ := session.Wait(context.Background())
	if len(result.Tail) > OutputTailLimit {
		t.Fatalf("tail length = %d, want <= %d", len(result.Tail), OutputTailLimit)
	}
	if len(result.Tail) == 0 {
		t.Fatal("tail is empty")
	}
}

func TestScriptAdapterCancelKillsProcess(t *testing.T) {
	t.Parallel()

	adapter := &scriptAdapter{script: "sleep 30"}
	session, err := adapter.Start(context.Background(), StartRequest{Contract: testContract(), Dir: t.TempDir(), Prompt: "p"})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	started := time.Now()
	if err := session.Cancel(context.Background()); err != nil {
		t.Fatalf("Cancel() error = %v", err)
	}
	result, _ := session.Wait(context.Background())
	if time.Since(started) > 10*time.Second {
		t.Fatal("cancelled session kept running")
	}
	if result.ExitCode == 0 {
		t.Fatalf("cancelled result = %#v", result)
	}
}

func TestCLIAdapterReportsMissingBinary(t *testing.T) {
	t.Parallel()

	adapter := &cliAdapter{slug: "codex", binary: "state-runner-definitely-missing-binary", args: func(prompt string) []string { return []string{prompt} }}
	err := adapter.Validate(testContract())
	if !errors.Is(err, ErrAdapterUnavailable) {
		t.Fatalf("Validate() error = %v, want ErrAdapterUnavailable", err)
	}
	if _, err := adapter.Start(context.Background(), StartRequest{Contract: testContract(), Dir: t.TempDir(), Prompt: "p"}); !errors.Is(err, ErrAdapterUnavailable) {
		t.Fatalf("Start() error = %v, want ErrAdapterUnavailable", err)
	}
}

// shippedAdapterNames are the adapters the runner ships. The server accepts any
// harness-shaped label, so this registry is the gate that decides which adapter
// names can actually launch a process.
func shippedAdapterNames() []string {
	return []string{"codex", "claude-code", "opencode", "pi-agent", "deepseek-harness"}
}

func TestDefaultAdaptersIncludePiAgent(t *testing.T) {
	t.Parallel()

	adapter, ok := DefaultAdapters()["pi-agent"]
	if !ok {
		t.Fatal("pi-agent adapter missing")
	}
	cli, ok := adapter.(*cliAdapter)
	if !ok {
		t.Fatalf("pi-agent adapter has type %T", adapter)
	}
	if cli.binary != "pi" {
		t.Fatalf("binary = %q", cli.binary)
	}
	if got := cli.args("do the thing"); !reflect.DeepEqual(got, []string{"-p", "do the thing"}) {
		t.Fatalf("args = %#v", got)
	}
}

func TestDefaultAdaptersIncludeDeepSeekHarness(t *testing.T) {
	t.Parallel()

	adapter, ok := DefaultAdapters()["deepseek-harness"]
	if !ok {
		t.Fatal("deepseek-harness adapter missing")
	}
	cli, ok := adapter.(*cliAdapter)
	if !ok {
		t.Fatalf("deepseek-harness adapter has type %T", adapter)
	}
	if cli.binary != "dsh" {
		t.Fatalf("binary = %q", cli.binary)
	}
	if got := cli.args("do the thing"); !reflect.DeepEqual(got, []string{"--profile", "headless", "do the thing"}) {
		t.Fatalf("args = %#v", got)
	}
}

// The shipped entries resolve their binary from PATH, so an installation that
// cannot see the CLI must fail as adapter_unavailable instead of launching.
func TestShippedAdaptersReportMissingBinary(t *testing.T) {
	// Not parallel: mutates the process environment.
	t.Setenv("PATH", t.TempDir())

	for _, name := range shippedAdapterNames() {
		adapter, ok := DefaultAdapters()[name]
		if !ok {
			t.Fatalf("DefaultAdapters() misses %s", name)
		}
		if err := adapter.Validate(testContract()); !errors.Is(err, ErrAdapterUnavailable) {
			t.Fatalf("%s Validate() error = %v, want ErrAdapterUnavailable", name, err)
		}
		if _, err := adapter.Start(context.Background(), StartRequest{Contract: testContract(), Dir: t.TempDir(), Prompt: "p"}); !errors.Is(err, ErrAdapterUnavailable) {
			t.Fatalf("%s Start() error = %v, want ErrAdapterUnavailable", name, err)
		}
	}
}

// TestShippedAdaptersPassPromptAsSingleArgvElement records what the launched
// process actually receives. The prompt stays one argv element even with shell
// metacharacters in it, because no adapter builds a command string.
func TestShippedAdaptersPassPromptAsSingleArgvElement(t *testing.T) {
	// Not parallel: mutates the process environment.
	dir := t.TempDir()
	t.Setenv("PATH", dir)
	prompt := "review the metrics; rm -rf / # not a shell string"

	cases := []struct {
		name   string
		binary string
		want   []string
	}{
		{name: "pi-agent", binary: "pi", want: []string{"-p", prompt}},
		{name: "deepseek-harness", binary: "dsh", want: []string{"--profile", "headless", prompt}},
	}
	for _, testCase := range cases {
		dump := filepath.Join(dir, testCase.name+".argv")
		script := "#!/bin/sh\nprintf '%s\\n' \"$#\" \"$@\" > " + dump + "\n"
		if err := os.WriteFile(filepath.Join(dir, testCase.binary), []byte(script), 0o755); err != nil {
			t.Fatalf("write fake %s: %v", testCase.binary, err)
		}

		adapter := DefaultAdapters()[testCase.name]
		if adapter == nil {
			t.Fatalf("DefaultAdapters() misses %s", testCase.name)
		}
		session, err := adapter.Start(context.Background(), StartRequest{Contract: testContract(), Dir: dir, Prompt: prompt})
		if err != nil {
			t.Fatalf("%s Start() error = %v", testCase.name, err)
		}
		if _, err := session.Wait(context.Background()); err != nil {
			t.Fatalf("%s Wait() error = %v", testCase.name, err)
		}
		recorded, err := os.ReadFile(dump)
		if err != nil {
			t.Fatalf("%s did not record its argv: %v", testCase.name, err)
		}
		lines := strings.Split(strings.TrimSuffix(string(recorded), "\n"), "\n")
		if lines[0] != strconv.Itoa(len(testCase.want)) {
			t.Fatalf("%s argc = %s, argv = %#v", testCase.name, lines[0], lines[1:])
		}
		if got := lines[1:]; !reflect.DeepEqual(got, testCase.want) {
			t.Fatalf("%s argv = %#v, want %#v", testCase.name, got, testCase.want)
		}
	}
}

// A policy names its adapter; the server only checks the label shape, which the
// new adapter names satisfy. A typo passes that check as well, so the label must
// also be registered in the runner.
func TestPolicyValidationAcceptsNewAdapterLabels(t *testing.T) {
	t.Parallel()

	for _, adapter := range []string{"pi-agent", "deepseek-harness"} {
		if _, ok := DefaultAdapters()[adapter]; !ok {
			t.Fatalf("adapter %q is not registered, so a policy naming it could never run", adapter)
		}
		policy := state.ExecutionPolicy{
			Name:                "review",
			Adapter:             adapter,
			Mode:                state.ExecutionModeSupervised,
			AllowedCapabilities: []string{state.CapabilityReadRepository},
			TimeoutMinutes:      30,
		}
		if err := state.ValidPolicyConfiguration(policy); err != nil {
			t.Fatalf("ValidPolicyConfiguration(adapter %q) error = %v", adapter, err)
		}
	}
}

func TestDefaultAdaptersContainShippedAdaptersOnly(t *testing.T) {
	// Not parallel: mutates the process environment.
	t.Setenv("STATE_RUNNER_TEST_ADAPTER", "")
	adapters := DefaultAdapters()
	for _, name := range shippedAdapterNames() {
		if _, ok := adapters[name]; !ok {
			t.Fatalf("DefaultAdapters() misses %s", name)
		}
	}
	for name, adapter := range adapters {
		if adapter.Name() != name {
			t.Fatalf("adapter registered as %s reports the name %q", name, adapter.Name())
		}
	}
	if _, ok := adapters["unknown-agent"]; ok {
		t.Fatal("DefaultAdapters() registers an adapter it cannot launch")
	}
	if _, ok := adapters["script"]; ok {
		t.Fatal("DefaultAdapters() exposes the test adapter without the env gate")
	}

	t.Setenv("STATE_RUNNER_TEST_ADAPTER", "1")
	t.Setenv("STATE_RUNNER_TEST_SCRIPT", "echo gated")
	gated := DefaultAdapters()
	script, ok := gated["script"]
	if !ok {
		t.Fatal("DefaultAdapters() misses the gated script adapter")
	}
	session, err := script.Start(context.Background(), StartRequest{Contract: testContract(), Dir: t.TempDir(), Prompt: "p"})
	if err != nil {
		t.Fatalf("script Start() error = %v", err)
	}
	result, _ := session.Wait(context.Background())
	if !strings.Contains(result.Tail, "gated") {
		t.Fatalf("script tail = %q", result.Tail)
	}
}
