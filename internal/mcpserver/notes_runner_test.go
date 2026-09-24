package mcpserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/nicremo/state/internal/state"
)

// A runner reads the reminder it executes, never the owner's notes.
func TestRunnerExecutionContextHidesNotes(t *testing.T) {
	fixture := newTestMCPFixture(t)
	project, _, _, materialized := mustMaterializeRun(t, fixture)
	runnerCredential := pairRunner(t, fixture, "Mac mini")
	if _, err := fixture.state.RegisterRunner(context.Background(), runnerCredential.Actor, state.RegisterRunnerInput{
		DisplayName: "Mac mini", Projects: []string{project.ID}, Adapters: []string{"codex"}, ClientRequestID: uuid.NewString(),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.state.CreateNote(context.Background(), fixture.owner, state.CreateNoteInput{Document: "# Geheim\nTOPSECRET", ClientRequestID: uuid.NewString()}); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.Handle("/mcp", fixture.handler)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	runner := connectToolSession(t, srv.URL+"/mcp", runnerCredential.Token)
	callTool(t, runner, "claim_agent_run", map[string]any{"wait_seconds": 0})
	result := callTool(t, runner, "get_execution_context", map[string]any{"run_id": materialized.ID})
	raw, _ := json.Marshal(result["changes"])
	if strings.Contains(string(raw), "TOPSECRET") {
		t.Fatalf("runner sees note content via get_execution_context: %s", raw)
	}
}
