package runner

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/nicremo/state/internal/state"
)

// TestT3LiveThread runs one real round through the installed T3 Code. It
// starts a visible thread in the T3 project of STATE_T3_LIVE_DIR, so it only
// runs when that variable is set.
func TestT3LiveThread(t *testing.T) {
	dir := os.Getenv("STATE_T3_LIVE_DIR")
	if dir == "" {
		t.Skip("set STATE_T3_LIVE_DIR to a folder inside a T3 Code project")
	}
	contract := fullRightsContract()
	contract.Objective = "State E2E: answer with OK"
	contract.ContractHash = contract.ComputeHash()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	adapter := &t3Adapter{}
	session, err := adapter.Start(ctx, StartRequest{Contract: contract, Dir: dir, Prompt: "This is an automated connectivity test from State. Reply with exactly the word OK. Do not use any tools."})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	result, err := session.Wait(ctx)
	if err != nil {
		t.Fatalf("Wait() error = %v", err)
	}
	t.Logf("exit=%d thread=%s link=%s text=%q", result.ExitCode, result.HarnessSessionID, result.HarnessLink, result.Text)
	if result.ExitCode != 0 || !strings.Contains(strings.ToUpper(result.Text), "OK") {
		t.Fatalf("result = %+v", result)
	}
	if !state.ValidHarnessSessionID(result.HarnessSessionID) || !strings.HasPrefix(result.HarnessLink, "https://app.t3.codes/threads/") {
		t.Fatalf("thread %q link %q", result.HarnessSessionID, result.HarnessLink)
	}

	// A later round continues the same thread.
	contract.ResumeSessionID = result.HarnessSessionID
	contract.ContractHash = contract.ComputeHash()
	session, err = adapter.Start(ctx, StartRequest{Contract: contract, Dir: dir, Prompt: "Reply with exactly the word AGAIN. Do not use any tools."})
	if err != nil {
		t.Fatalf("second Start() error = %v", err)
	}
	second, err := session.Wait(ctx)
	if err != nil {
		t.Fatalf("second Wait() error = %v", err)
	}
	t.Logf("second exit=%d thread=%s text=%q", second.ExitCode, second.HarnessSessionID, second.Text)
	if second.ExitCode != 0 || !strings.Contains(strings.ToUpper(second.Text), "AGAIN") || second.HarnessSessionID != result.HarnessSessionID || second.HarnessLink != result.HarnessLink {
		t.Fatalf("second = %+v", second)
	}
}
