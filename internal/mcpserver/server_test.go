package mcpserver

import (
	"context"
	"crypto/ed25519"
	"net/http"
	"net/http/httptest"
	"sort"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	stateauth "github.com/nicremo/state/internal/auth"
	"github.com/nicremo/state/internal/state"
	"github.com/nicremo/state/internal/store"
	"github.com/pocketbase/pocketbase"
)

func TestMCPServerNegotiatesListsToolsAndCreatesAuditedReminder(t *testing.T) {
	t.Parallel()

	handler, token := newTestMCPHandler(t)
	mux := http.NewServeMux()
	mux.Handle("/mcp", handler)
	httpServer := httptest.NewServer(mux)
	t.Cleanup(httpServer.Close)

	client := mcp.NewClient(&mcp.Implementation{Name: "state-test", Version: "1.0.0"}, nil)
	session, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{
		Endpoint:             httpServer.URL + "/mcp",
		HTTPClient:           &http.Client{Transport: bearerTransport{token: token}},
		DisableStandaloneSSE: true,
	}, nil)
	if err != nil {
		t.Fatalf("Connect() error = %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })
	if session.InitializeResult().Instructions == "" {
		t.Fatal("MCP instructions are empty")
	}
	if session.InitializeResult().ProtocolVersion != "2026-07-28" {
		t.Fatalf("protocol version = %q, want 2026-07-28", session.InitializeResult().ProtocolVersion)
	}
	toolsResult, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools() error = %v", err)
	}
	names := make([]string, 0, len(toolsResult.Tools))
	for _, tool := range toolsResult.Tools {
		names = append(names, tool.Name)
	}
	sort.Strings(names)
	want := []string{
		"add_comment",
		"claim_agent_run",
		"complete_agent_run",
		"complete_occurrence",
		"create_reminder",
		"get_briefing",
		"get_changes",
		"get_execution_context",
		"get_reminder",
		"report_agent_run_event",
		"request_agent_approval",
		"search_reminders",
		"snooze_occurrence",
		"update_reminder",
	}
	if len(names) != len(want) {
		t.Fatalf("tool names = %#v, want %#v", names, want)
	}
	for index := range want {
		if names[index] != want[index] {
			t.Fatalf("tool names = %#v, want %#v", names, want)
		}
	}

	createResult, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "create_reminder",
		Arguments: map[string]any{
			"title":             "Prepare MCP review",
			"description":       "Use the latest metrics.",
			"client_request_id": "01989f08-115a-7d75-bce4-a3c795945f7f",
			"source_text":       "Remind me on 17 August at 9",
			"schedule": map[string]any{
				"local_date": "2026-08-17",
				"local_time": "09:00",
				"time_zone":  "Europe/Copenhagen",
				"mode":       "floating",
			},
		},
	})
	if err != nil {
		t.Fatalf("CallTool(create_reminder) error = %v", err)
	}
	if createResult.IsError {
		t.Fatalf("create_reminder returned tool error: %#v", createResult.Content)
	}
	structured, ok := createResult.StructuredContent.(map[string]any)
	if !ok {
		t.Fatalf("create structured content type = %T", createResult.StructuredContent)
	}
	reminderValue, ok := structured["reminder"].(map[string]any)
	if !ok {
		t.Fatalf("create structured reminder = %#v", structured["reminder"])
	}
	reminderID, _ := reminderValue["id"].(string)
	if reminderID == "" {
		t.Fatal("created reminder ID is empty")
	}

	detailResult, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "get_reminder",
		Arguments: map[string]any{"reminder_id": reminderID},
	})
	if err != nil || detailResult.IsError {
		t.Fatalf("CallTool(get_reminder) = %#v, %v", detailResult, err)
	}
	detail, ok := detailResult.StructuredContent.(map[string]any)
	if !ok {
		t.Fatalf("detail structured content type = %T", detailResult.StructuredContent)
	}
	history, ok := detail["history"].([]any)
	if !ok || len(history) != 1 {
		t.Fatalf("detail history = %#v", detail["history"])
	}
	event, ok := history[0].(map[string]any)
	if !ok {
		t.Fatalf("history event = %#v", history[0])
	}
	actor, ok := event["actor"].(map[string]any)
	if !ok || actor["harness"] != "claude-code" || actor["device_name"] != "MacBook" {
		t.Fatalf("history actor = %#v", event["actor"])
	}
}

func TestMCPServerRejectsMissingBearerToken(t *testing.T) {
	t.Parallel()

	handler, _ := newTestMCPHandler(t)
	httpServer := httptest.NewServer(handler)
	t.Cleanup(httpServer.Close)
	client := mcp.NewClient(&mcp.Implementation{Name: "state-test", Version: "1.0.0"}, nil)
	_, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{
		Endpoint:             httpServer.URL,
		DisableStandaloneSSE: true,
		MaxRetries:           -1,
	}, nil)
	if err == nil {
		t.Fatal("Connect() succeeded without bearer token")
	}
}

func newTestMCPHandler(t *testing.T) (http.Handler, string) {
	t.Helper()

	fixture := newTestMCPFixture(t)
	return fixture.handler, fixture.pairHarness(t, "claude-code", "Claude Code", "MacBook")
}

// testMCPFixture is a booted server plus the owner credential, so a test can
// pair as many agents as it needs.
type testMCPFixture struct {
	handler    http.Handler
	auth       *stateauth.Manager
	state      *state.Service
	owner      state.Actor
	ownerToken string
}

// pairHarness creates a one-time code as the owner and exchanges it, which is
// exactly what statectl does. It returns the agent's bearer token.
func (fixture testMCPFixture) pairHarness(t *testing.T, harness string, displayName string, deviceName string) string {
	t.Helper()

	pairing, err := fixture.auth.CreatePairingCode(context.Background(), fixture.owner, stateauth.PairingCodeRequest{
		Harness:     harness,
		DisplayName: displayName,
		DeviceName:  deviceName,
	})
	if err != nil {
		t.Fatalf("CreatePairingCode(%s) error = %v", harness, err)
	}
	credential, err := fixture.auth.ExchangePairingCode(context.Background(), pairing.Code)
	if err != nil {
		t.Fatalf("ExchangePairingCode(%s) error = %v", harness, err)
	}
	return credential.Token
}

func newTestMCPFixture(t *testing.T) testMCPFixture {
	t.Helper()

	app := pocketbase.NewWithConfig(pocketbase.Config{
		DefaultDataDir:   t.TempDir(),
		HideStartBanner:  true,
		DataMaxOpenConns: 1,
		DataMaxIdleConns: 1,
		AuxMaxOpenConns:  1,
		AuxMaxIdleConns:  1,
	})
	if err := app.Bootstrap(); err != nil {
		t.Fatalf("Bootstrap() error = %v", err)
	}
	t.Cleanup(func() {
		if err := app.ResetBootstrapState(); err != nil {
			t.Errorf("ResetBootstrapState() error = %v", err)
		}
	})
	seed := make([]byte, ed25519.SeedSize)
	for index := range seed {
		seed[index] = byte(index + 21)
	}
	repository, err := store.NewPocketBaseRepository(app, ed25519.NewKeyFromSeed(seed))
	if err != nil {
		t.Fatalf("NewPocketBaseRepository() error = %v", err)
	}
	authManager, err := stateauth.NewManager(app, "bootstrap-secret")
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	ownerCredential, err := authManager.BootstrapOwner(context.Background(), "bootstrap-secret", stateauth.OwnerBootstrapRequest{
		DisplayName: "Fabian",
		DeviceName:  "iPhone",
	})
	if err != nil {
		t.Fatalf("BootstrapOwner() error = %v", err)
	}
	owner, err := authManager.Authenticate(context.Background(), ownerCredential.Token)
	if err != nil {
		t.Fatalf("Authenticate(owner) error = %v", err)
	}
	stateService := state.NewService(repository)
	handler := NewHandler(Config{
		Auth:    authManager,
		State:   stateService,
		Version: "test-version",
	})
	return testMCPFixture{handler: handler, auth: authManager, state: stateService, owner: owner, ownerToken: ownerCredential.Token}
}

type bearerTransport struct {
	token string
}

func (transport bearerTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	clone := request.Clone(request.Context())
	clone.Header = request.Header.Clone()
	clone.Header.Set("Authorization", "Bearer "+transport.token)
	return http.DefaultTransport.RoundTrip(clone)
}
