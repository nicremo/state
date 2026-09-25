package mcpserver

import (
	"bytes"
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestMCPNoteAttachmentProcessingAndRelations(t *testing.T) {
	t.Parallel()
	fixture := newTestMCPFixture(t)
	mux := http.NewServeMux()
	mux.Handle("/mcp", fixture.handler)
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	session := connectToolSession(t, server.URL+"/mcp", fixture.pairHarness(t, "codex", "Codex", "MacBook"))

	created := callTool(t, session, "create_note", map[string]any{
		"capture":           "image",
		"client_request_id": "01989f08-3333-7000-8000-000000000001",
		"source_text":       "Häng das Foto aus meinem Notizbuch an",
	})
	note := created["note"].(map[string]any)
	noteID := note["id"].(string)
	if note["capture"] != "image" {
		t.Fatalf("created = %#v", note)
	}

	jpeg := append([]byte{0xFF, 0xD8, 0xFF, 0xE0}, bytes.Repeat([]byte("n"), 100)...)
	attached := callTool(t, session, "add_note_attachment", map[string]any{
		"note_id":           noteID,
		"mime_type":         "image/jpeg",
		"content_base64":    base64.StdEncoding.EncodeToString(jpeg),
		"client_request_id": "01989f08-3333-7000-8000-000000000002",
		"source_text":       "Häng das Foto an",
	})
	if attached["stored"] != true {
		t.Fatalf("attach = %#v", attached)
	}
	fake, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "add_note_attachment", Arguments: map[string]any{
		"note_id": noteID, "mime_type": "image/jpeg", "content_base64": base64.StdEncoding.EncodeToString([]byte("<html>")),
		"client_request_id": "01989f08-3333-7000-8000-000000000003", "source_text": "x",
	}})
	if err == nil && !fake.IsError {
		t.Fatal("a file that is not a JPEG was attached as one")
	}

	processing := callTool(t, session, "process_note", map[string]any{"note_id": noteID, "client_request_id": "01989f08-3333-7000-8000-000000000004"})
	if processing["processing"].(map[string]any)["status"] != "not_configured" {
		t.Fatalf("process_note without key = %#v", processing)
	}
	status := callTool(t, session, "get_note_processing", map[string]any{"note_id": noteID})
	if len(status["attachments"].([]any)) != 1 {
		t.Fatalf("get_note_processing = %#v", status)
	}
	related := callTool(t, session, "list_related_notes", map[string]any{"note_id": noteID})
	if related["relations"] == nil {
		t.Fatalf("list_related_notes = %#v", related)
	}
	detail := callTool(t, session, "get_note", map[string]any{"note_id": noteID})
	if len(detail["note"].(map[string]any)["attachments"].([]any)) != 1 {
		t.Fatalf("get_note has no attachments: %#v", detail["note"])
	}

	runnerSession := connectToolSession(t, server.URL+"/mcp", pairRunner(t, fixture, "Mac mini").Token)
	for _, tool := range []string{"get_note_processing", "list_related_notes", "process_note", "add_note_attachment"} {
		result, err := runnerSession.CallTool(context.Background(), &mcp.CallToolParams{Name: tool, Arguments: map[string]any{
			"note_id": noteID, "client_request_id": "01989f08-3333-7000-8000-00000000000" + string(rune('5'+len(tool)%4)),
			"mime_type": "image/jpeg", "content_base64": base64.StdEncoding.EncodeToString(jpeg), "source_text": "x",
		}})
		if err == nil && !result.IsError {
			t.Fatalf("runner used %s", tool)
		}
	}
}
