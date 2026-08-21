package mcpserver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	stateauth "github.com/nicremo/state/internal/auth"
	"github.com/nicremo/state/internal/state"
)

// pairRunner drives the real pairing flow for a runner actor and returns its
// credential.
func pairRunner(t *testing.T, fixture testMCPFixture, displayName string) stateauth.Credential {
	t.Helper()

	pairing, err := fixture.auth.CreatePairingCode(context.Background(), fixture.owner, stateauth.PairingCodeRequest{
		Kind:        state.ActorKindRunner,
		DisplayName: displayName,
	})
	if err != nil {
		t.Fatalf("CreatePairingCode(runner) error = %v", err)
	}
	credential, err := fixture.auth.ExchangePairingCode(context.Background(), pairing.Code)
	if err != nil {
		t.Fatalf("ExchangePairingCode(runner) error = %v", err)
	}
	if credential.Actor.Kind != state.ActorKindRunner {
		t.Fatalf("paired actor kind = %q", credential.Actor.Kind)
	}
	return credential
}

func connectToolSession(t *testing.T, endpoint string, token string) *mcp.ClientSession {
	t.Helper()

	client := mcp.NewClient(&mcp.Implementation{Name: "state-test", Version: "1.0.0"}, nil)
	session, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{
		Endpoint:             endpoint,
		HTTPClient:           &http.Client{Transport: bearerTransport{token: token}},
		DisableStandaloneSSE: true,
	}, nil)
	if err != nil {
		t.Fatalf("Connect() error = %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

func callTool(t *testing.T, session *mcp.ClientSession, name string, arguments map[string]any) map[string]any {
	t.Helper()

	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: arguments})
	if err != nil {
		t.Fatalf("CallTool(%s) error = %v", name, err)
	}
	if result.IsError {
		t.Fatalf("CallTool(%s) tool error: %s", name, toolErrorText(result))
	}
	structured, ok := result.StructuredContent.(map[string]any)
	if !ok {
		t.Fatalf("CallTool(%s) structured content type = %T", name, result.StructuredContent)
	}
	return structured
}

// runFrom extracts the run envelope of the execution tools.
func runFrom(t *testing.T, structured map[string]any) map[string]any {
	t.Helper()

	run, ok := structured["run"].(map[string]any)
	if !ok {
		t.Fatalf("structured run = %#v", structured["run"])
	}
	return run
}

func revisionOf(t *testing.T, run map[string]any) int64 {
	t.Helper()

	revision, ok := run["revision"].(float64)
	if !ok || revision < 1 {
		t.Fatalf("run revision = %#v", run["revision"])
	}
	return int64(revision)
}

// mustMaterializeRun builds a project, a policy with occurrence auto-complete,
// and a due scheduled reminder, then materializes the eligible run.
func mustMaterializeRun(t *testing.T, fixture testMCPFixture) (state.Project, state.ExecutionPolicy, state.Reminder, state.AgentRun) {
	t.Helper()

	project, err := fixture.state.CreateProject(context.Background(), fixture.owner, state.CreateProjectInput{
		Name:            "customer-api",
		RootPathHint:    "~/src/customer-api",
		ClientRequestID: uuid.NewString(),
	})
	if err != nil {
		t.Fatalf("CreateProject() error = %v", err)
	}
	policy, err := fixture.state.CreatePolicy(context.Background(), fixture.owner, state.CreatePolicyInput{
		Name:                        "nightly-review",
		ProjectID:                   project.ID,
		Adapter:                     "codex",
		Mode:                        state.ExecutionModeSupervised,
		AllowedCapabilities:         []string{state.CapabilityReadRepository, state.CapabilityRunTests},
		MarkOccurrenceDoneOnSuccess: true,
		TimeoutMinutes:              30,
		ClientRequestID:             uuid.NewString(),
	})
	if err != nil {
		t.Fatalf("CreatePolicy() error = %v", err)
	}
	yesterday := time.Now().UTC().Add(-26 * time.Hour)
	reminder, err := fixture.state.CreateReminder(context.Background(), fixture.owner, state.CreateReminderInput{
		Title:             "Review the nightly metrics",
		Description:       "All checks must pass",
		ExecutionPolicyID: &policy.ID,
		ClientRequestID:   uuid.NewString(),
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
	created, err := fixture.state.MaterializeEligibleRuns(context.Background(), time.Now().UTC())
	if err != nil {
		t.Fatalf("MaterializeEligibleRuns() error = %v", err)
	}
	if len(created) != 1 {
		t.Fatalf("MaterializeEligibleRuns() created %d runs, want 1", len(created))
	}
	return project, policy, reminder, created[0]
}

func TestMCPRunnerLifecycleOverExecutionTools(t *testing.T) {
	t.Parallel()

	fixture := newTestMCPFixture(t)
	project, policy, reminder, materialized := mustMaterializeRun(t, fixture)

	runnerCredential := pairRunner(t, fixture, "Mac mini")
	if _, err := fixture.state.RegisterRunner(context.Background(), runnerCredential.Actor, state.RegisterRunnerInput{
		DisplayName:     "Mac mini",
		Projects:        []string{project.ID},
		Adapters:        []string{"codex"},
		ClientRequestID: uuid.NewString(),
	}); err != nil {
		t.Fatalf("RegisterRunner() error = %v", err)
	}

	mux := http.NewServeMux()
	mux.Handle("/mcp", fixture.handler)
	httpServer := httptest.NewServer(mux)
	t.Cleanup(httpServer.Close)
	runner := connectToolSession(t, httpServer.URL+"/mcp", runnerCredential.Token)

	claimed := runFrom(t, callTool(t, runner, "claim_agent_run", map[string]any{"wait_seconds": 0}))
	if claimed["id"] != materialized.ID || claimed["status"] != "claimed" {
		t.Fatalf("claimed run = %#v", claimed)
	}
	if claimed["runner_id"] != runnerCredential.Actor.ID {
		t.Fatalf("claimed runner = %#v", claimed["runner_id"])
	}
	contract, ok := claimed["task_contract"].(map[string]any)
	if !ok || contract["run_id"] != materialized.ID || contract["policy_id"] != policy.ID || contract["contract_hash"] == "" {
		t.Fatalf("task contract = %#v", claimed["task_contract"])
	}

	runID := materialized.ID
	started := runFrom(t, callTool(t, runner, "report_agent_run_event", map[string]any{
		"run_id":            runID,
		"event":             "started",
		"detail":            "adapter launched",
		"expected_revision": revisionOf(t, claimed),
	}))
	if started["status"] != "running" {
		t.Fatalf("started run = %#v", started)
	}

	progress := runFrom(t, callTool(t, runner, "report_agent_run_event", map[string]any{
		"run_id":            runID,
		"event":             "progress",
		"detail":            "tests green",
		"expected_revision": revisionOf(t, started),
	}))
	if progress["status"] != "running" {
		t.Fatalf("progress run = %#v", progress)
	}

	heartbeat := runFrom(t, callTool(t, runner, "report_agent_run_event", map[string]any{
		"run_id":            runID,
		"event":             "heartbeat",
		"expected_revision": revisionOf(t, progress),
	}))
	if revisionOf(t, heartbeat) != revisionOf(t, progress)+1 {
		t.Fatalf("heartbeat revision = %v, want %d", heartbeat["revision"], revisionOf(t, progress)+1)
	}

	contextResult := callTool(t, runner, "get_execution_context", map[string]any{"run_id": runID})
	contextRun := runFrom(t, contextResult)
	if contextRun["id"] != runID {
		t.Fatalf("context run = %#v", contextRun)
	}
	contextReminder, ok := contextResult["reminder"].(map[string]any)
	if !ok || contextReminder["id"] != reminder.ID {
		t.Fatalf("context reminder = %#v", contextResult["reminder"])
	}
	contextPolicy, ok := contextResult["policy"].(map[string]any)
	if !ok || contextPolicy["id"] != policy.ID {
		t.Fatalf("context policy = %#v", contextResult["policy"])
	}
	if _, ok := contextResult["changes"].([]any); !ok {
		t.Fatalf("context changes = %#v", contextResult["changes"])
	}
	if cursor, ok := contextResult["cursor"].(float64); !ok || cursor < float64(materialized.ContextCursor) {
		t.Fatalf("context cursor = %#v, want >= %d", contextResult["cursor"], materialized.ContextCursor)
	}

	completed := runFrom(t, callTool(t, runner, "complete_agent_run", map[string]any{
		"run_id":            runID,
		"outcome":           "succeeded",
		"result_summary":    "review landed",
		"exit_code":         0,
		"expected_revision": revisionOf(t, heartbeat),
		"client_request_id": uuid.NewString(),
	}))
	if completed["status"] != "succeeded" || completed["result_summary"] != "review landed" {
		t.Fatalf("completed run = %#v", completed)
	}

	// The policy's completion rule closed the originating occurrence.
	occurrences, err := fixture.state.ListOccurrences(context.Background(), reminder.ID, state.OccurrenceListOptions{})
	if err != nil || len(occurrences) != 1 {
		t.Fatalf("ListOccurrences() = %#v, %v", occurrences, err)
	}
	if occurrences[0].Status != state.OccurrenceStatusCompleted {
		t.Fatalf("occurrence status = %q, want completed", occurrences[0].Status)
	}
}

func TestMCPExecutionToolsEnforceActorKinds(t *testing.T) {
	t.Parallel()

	fixture := newTestMCPFixture(t)
	project, _, _, materialized := mustMaterializeRun(t, fixture)

	runnerCredential := pairRunner(t, fixture, "Mac mini")
	if _, err := fixture.state.RegisterRunner(context.Background(), runnerCredential.Actor, state.RegisterRunnerInput{
		DisplayName:     "Mac mini",
		Projects:        []string{project.ID},
		Adapters:        []string{"codex"},
		ClientRequestID: uuid.NewString(),
	}); err != nil {
		t.Fatalf("RegisterRunner() error = %v", err)
	}

	mux := http.NewServeMux()
	mux.Handle("/mcp", fixture.handler)
	httpServer := httptest.NewServer(mux)
	t.Cleanup(httpServer.Close)

	// The owner reads an execution context but must not drive the runner lifecycle.
	owner := connectToolSession(t, httpServer.URL+"/mcp", fixture.ownerToken)
	ownerContext := callTool(t, owner, "get_execution_context", map[string]any{"run_id": materialized.ID})
	if runFrom(t, ownerContext)["id"] != materialized.ID {
		t.Fatalf("owner context run = %#v", ownerContext)
	}
	ownerClaim, err := owner.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "claim_agent_run",
		Arguments: map[string]any{"wait_seconds": 0},
	})
	if err != nil {
		t.Fatalf("owner claim error = %v", err)
	}
	if !ownerClaim.IsError || !strings.Contains(toolErrorText(ownerClaim), "forbidden") {
		t.Fatalf("owner claim result = %#v, want forbidden tool error", ownerClaim)
	}

	// Runners never touch reminders.
	runner := connectToolSession(t, httpServer.URL+"/mcp", runnerCredential.Token)
	runnerCreate, err := runner.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "create_reminder",
		Arguments: map[string]any{
			"title":             "Forged by a runner",
			"client_request_id": uuid.NewString(),
			"source_text":       "create a reminder",
		},
	})
	if err != nil {
		t.Fatalf("runner create_reminder error = %v", err)
	}
	if !runnerCreate.IsError || !strings.Contains(toolErrorText(runnerCreate), "forbidden") {
		t.Fatalf("runner create_reminder result = %#v, want forbidden tool error", runnerCreate)
	}

	// Harnesses keep their reminder tools but stay off the runner surface entirely.
	harness := connectToolSession(t, httpServer.URL+"/mcp", fixture.pairHarness(t, "codex", "Codex", "MacBook"))
	harnessCalls := []struct {
		tool      string
		arguments map[string]any
	}{
		{"claim_agent_run", map[string]any{"wait_seconds": 0}},
		{"report_agent_run_event", map[string]any{"run_id": materialized.ID, "event": "started", "expected_revision": 1}},
		{"complete_agent_run", map[string]any{"run_id": materialized.ID, "outcome": "succeeded", "exit_code": 0, "expected_revision": 1, "client_request_id": uuid.NewString()}},
		{"request_agent_approval", map[string]any{"run_id": materialized.ID, "capability": "deploy", "expected_revision": 1}},
		{"get_execution_context", map[string]any{"run_id": materialized.ID}},
	}
	for _, call := range harnessCalls {
		result, err := harness.CallTool(context.Background(), &mcp.CallToolParams{
			Name:      call.tool,
			Arguments: call.arguments,
		})
		if err != nil {
			t.Fatalf("harness %s error = %v", call.tool, err)
		}
		if !result.IsError || !strings.Contains(toolErrorText(result), "forbidden") {
			t.Fatalf("harness %s result = %#v, want forbidden tool error", call.tool, result)
		}
	}
	created := callTool(t, harness, "create_reminder", map[string]any{
		"title":             "Harness reminders still work",
		"client_request_id": uuid.NewString(),
		"source_text":       "remind me to check the gates",
	})
	if created["stored"] != true {
		t.Fatalf("harness create_reminder = %#v", created)
	}
}

func TestMCPRevokedRunnerLosesAccess(t *testing.T) {
	t.Parallel()

	fixture := newTestMCPFixture(t)
	runnerCredential := pairRunner(t, fixture, "Mac mini")

	mux := http.NewServeMux()
	mux.Handle("/mcp", fixture.handler)
	httpServer := httptest.NewServer(mux)
	t.Cleanup(httpServer.Close)

	if err := fixture.auth.RevokeActor(context.Background(), fixture.owner, runnerCredential.Actor.ID); err != nil {
		t.Fatalf("RevokeActor(runner) error = %v", err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "state-test", Version: "1.0.0"}, nil)
	if _, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{
		Endpoint:             httpServer.URL + "/mcp",
		HTTPClient:           &http.Client{Transport: bearerTransport{token: runnerCredential.Token}},
		DisableStandaloneSSE: true,
		MaxRetries:           -1,
	}, nil); err == nil {
		t.Fatal("revoked runner still reached the MCP endpoint")
	}
}
