package runner

import (
	"context"
	"errors"
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

func TestDefaultAdaptersContainShippedAdaptersOnly(t *testing.T) {
	// Not parallel: mutates the process environment.
	t.Setenv("STATE_RUNNER_TEST_ADAPTER", "")
	adapters := DefaultAdapters()
	for _, name := range []string{"codex", "claude-code", "opencode"} {
		if _, ok := adapters[name]; !ok {
			t.Fatalf("DefaultAdapters() misses %s", name)
		}
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
