package mcpserver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestMCPNoteToolsRoundTrip(t *testing.T) {
	t.Parallel()

	fixture := newTestMCPFixture(t)
	mux := http.NewServeMux()
	mux.Handle("/mcp", fixture.handler)
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	session := connectToolSession(t, server.URL+"/mcp", fixture.pairHarness(t, "codex", "Codex", "MacBook"))

	created := callTool(t, session, "create_note", map[string]any{
		"document":          "# Ideen für State\nNotizen mit Fotos und Audio",
		"client_request_id": "01989f08-2222-7000-8000-000000000001",
		"source_text":       "Leg eine Notiz mit meinen Ideen an",
	})
	if created["stored"] != true {
		t.Fatalf("create_note result = %#v", created)
	}
	note := created["note"].(map[string]any)
	noteID := note["id"].(string)
	if note["title"] != "Ideen für State" || note["title_source"] != "derived" {
		t.Fatalf("created note = %#v", note)
	}

	search := callTool(t, session, "search_notes", map[string]any{"query": "audio"})
	notes := search["notes"].([]any)
	if len(notes) != 1 || notes[0].(map[string]any)["id"] != noteID {
		t.Fatalf("search_notes = %#v", search)
	}
	if _, hasDocument := notes[0].(map[string]any)["document"]; hasDocument {
		t.Fatal("search_notes returned the full document; results must stay small")
	}
	recent := callTool(t, session, "search_notes", map[string]any{})
	if len(recent["notes"].([]any)) != 1 {
		t.Fatalf("search_notes without query = %#v", recent)
	}

	detail := callTool(t, session, "get_note", map[string]any{"note_id": noteID})
	if detail["note"].(map[string]any)["document"] == "" || len(detail["history"].([]any)) != 1 {
		t.Fatalf("get_note = %#v", detail)
	}

	updated := callTool(t, session, "update_note", map[string]any{
		"note_id":           noteID,
		"expected_revision": 1,
		"title":             "State Ideen",
		"client_request_id": "01989f08-2222-7000-8000-000000000002",
		"source_text":       "Nenn die Notiz State Ideen",
	})
	if updated["stored"] != true || updated["note"].(map[string]any)["title_source"] != "user" {
		t.Fatalf("update_note = %#v", updated)
	}

	stale, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "update_note", Arguments: map[string]any{
		"note_id":           noteID,
		"expected_revision": 1,
		"title":             "Zu spät",
		"client_request_id": "01989f08-2222-7000-8000-000000000003",
		"source_text":       "Zu spät",
	}})
	if err != nil {
		t.Fatalf("stale update transport error = %v", err)
	}
	if !stale.IsError || !strings.Contains(toolErrorText(stale), "revision") {
		t.Fatalf("stale update = %#v", stale)
	}
}

func TestMCPNoteToolsRejectRunners(t *testing.T) {
	t.Parallel()

	fixture := newTestMCPFixture(t)
	mux := http.NewServeMux()
	mux.Handle("/mcp", fixture.handler)
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	session := connectToolSession(t, server.URL+"/mcp", pairRunner(t, fixture, "Mac mini").Token)

	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "search_notes", Arguments: map[string]any{}})
	if err != nil {
		t.Fatalf("transport error = %v", err)
	}
	if !result.IsError || !strings.Contains(toolErrorText(result), "forbidden") {
		t.Fatalf("runner search_notes = %#v", result)
	}
}
