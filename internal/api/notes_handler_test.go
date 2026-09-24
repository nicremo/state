package api

import (
	"net/http"
	"strings"
	"testing"

	"github.com/nicremo/state/internal/state"
)

func TestNotesAPIEndToEnd(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t)
	owner := bootstrapOwner(t, handler)
	harness := pairActor(t, handler, owner.Token, map[string]any{
		"harness": "codex", "display_name": "Codex", "device_name": "MacBook",
	})
	runner := pairActor(t, handler, owner.Token, map[string]any{"kind": "runner", "display_name": "Mac mini"})

	createResponse := performJSONRequest(t, handler, http.MethodPost, "/api/v1/notes", owner.Token, map[string]any{
		"document":          "# Ideen\nState bekommt Notizen",
		"client_request_id": requestID(t),
	})
	if createResponse.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body = %s", createResponse.Code, createResponse.Body.String())
	}
	var note state.Note
	decodeResponse(t, createResponse, &note)
	if note.Title != "Ideen" || note.TitleSource != state.NoteFieldSourceDerived || note.Revision != 1 {
		t.Fatalf("created note = %#v", note)
	}

	listResponse := performJSONRequest(t, handler, http.MethodGet, "/api/v1/notes?q=notizen", harness.Token, nil)
	var list struct {
		Notes []state.Note `json:"notes"`
	}
	decodeResponse(t, listResponse, &list)
	if listResponse.Code != http.StatusOK || len(list.Notes) != 1 || list.Notes[0].ID != note.ID {
		t.Fatalf("list status = %d, notes = %#v", listResponse.Code, list.Notes)
	}

	updateResponse := performJSONRequest(t, handler, http.MethodPatch, "/api/v1/notes/"+note.ID, harness.Token, map[string]any{
		"document":          "# Ideen\nState bekommt Notizen und Fotos",
		"expected_revision": 1,
		"client_request_id": requestID(t),
	})
	if updateResponse.Code != http.StatusOK {
		t.Fatalf("update status = %d, body = %s", updateResponse.Code, updateResponse.Body.String())
	}
	decodeResponse(t, updateResponse, &note)
	if note.Revision != 2 {
		t.Fatalf("revision = %d, want 2", note.Revision)
	}

	conflictResponse := performJSONRequest(t, handler, http.MethodPatch, "/api/v1/notes/"+note.ID, harness.Token, map[string]any{
		"document":          "stale",
		"expected_revision": 1,
		"client_request_id": requestID(t),
	})
	if conflictResponse.Code != http.StatusConflict {
		t.Fatalf("conflict status = %d", conflictResponse.Code)
	}
	var conflict struct {
		Details struct {
			Server state.Note `json:"server"`
		} `json:"details"`
	}
	decodeResponse(t, conflictResponse, &conflict)
	if conflict.Details.Server.Revision != 2 {
		t.Fatalf("conflict did not return the server note: %#v", conflict)
	}

	agentArchive := performJSONRequest(t, handler, http.MethodPatch, "/api/v1/notes/"+note.ID, harness.Token, map[string]any{
		"archived": true, "expected_revision": 2, "client_request_id": requestID(t),
	})
	if agentArchive.Code != http.StatusForbidden {
		t.Fatalf("agent archive status = %d", agentArchive.Code)
	}
	ownerArchive := performJSONRequest(t, handler, http.MethodPatch, "/api/v1/notes/"+note.ID, owner.Token, map[string]any{
		"archived": true, "expected_revision": 2, "client_request_id": requestID(t),
	})
	if ownerArchive.Code != http.StatusOK {
		t.Fatalf("owner archive status = %d, body = %s", ownerArchive.Code, ownerArchive.Body.String())
	}

	if response := performJSONRequest(t, handler, http.MethodGet, "/api/v1/notes/"+note.ID, runner.Token, nil); response.Code != http.StatusForbidden {
		t.Fatalf("runner read status = %d, want 403", response.Code)
	}

	historyResponse := performJSONRequest(t, handler, http.MethodGet, "/api/v1/notes/"+note.ID+"/history", owner.Token, nil)
	var history struct {
		Events []state.AuditEvent `json:"events"`
	}
	decodeResponse(t, historyResponse, &history)
	if len(history.Events) != 3 || history.Events[2].Action != state.AuditActionNoteArchived {
		t.Fatalf("history = %#v", history.Events)
	}

	changesResponse := performJSONRequest(t, handler, http.MethodGet, "/api/v1/changes?after=0", owner.Token, nil)
	var changes struct {
		Changes []state.Change `json:"changes"`
	}
	decodeResponse(t, changesResponse, &changes)
	sawNote := false
	for _, change := range changes.Changes {
		if change.Event.NoteID == note.ID && change.Event.Action == state.AuditActionNoteCreated {
			sawNote = true
		}
	}
	if !sawNote {
		t.Fatalf("change feed has no note.created for %s", note.ID)
	}

	if response := performJSONRequest(t, handler, http.MethodGet, "/api/v1/notes/does-not-exist", owner.Token, nil); response.Code != http.StatusNotFound {
		t.Fatalf("missing note status = %d", response.Code)
	}
}

func TestExportIncludesNotesAndSkipsEventsWithoutReminder(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t)
	owner := bootstrapOwner(t, handler)
	mustCreateProjectAPI(t, handler, owner.Token, "export-project")
	createResponse := performJSONRequest(t, handler, http.MethodPost, "/api/v1/notes", owner.Token, map[string]any{
		"document":          "Exportiere mich",
		"client_request_id": requestID(t),
	})
	var note state.Note
	decodeResponse(t, createResponse, &note)

	exportResponse := performJSONRequest(t, handler, http.MethodGet, "/api/v1/export", owner.Token, nil)
	if exportResponse.Code != http.StatusOK {
		t.Fatalf("export status = %d, body = %s", exportResponse.Code, exportResponse.Body.String())
	}
	var exported Export
	decodeResponse(t, exportResponse, &exported)
	if len(exported.Notes) != 1 || exported.Notes[0].Note.ID != note.ID || len(exported.Notes[0].History) != 1 {
		t.Fatalf("exported notes = %#v", exported.Notes)
	}
}

func TestRunnersSeeNoNoteContentInChangesOrBriefing(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t)
	owner := bootstrapOwner(t, handler)
	runner := pairActor(t, handler, owner.Token, map[string]any{"kind": "runner", "display_name": "Mac mini"})
	performJSONRequest(t, handler, http.MethodPost, "/api/v1/notes", owner.Token, map[string]any{
		"document":          "# Geheim\nTOPSECRET-NOTE-CONTENT",
		"client_request_id": requestID(t),
	})

	for _, path := range []string{"/api/v1/changes?after=0", "/api/v1/briefing"} {
		response := performJSONRequest(t, handler, http.MethodGet, path, runner.Token, nil)
		if response.Code != http.StatusOK {
			t.Fatalf("%s status = %d", path, response.Code)
		}
		if strings.Contains(response.Body.String(), "TOPSECRET") || strings.Contains(response.Body.String(), "note_id") {
			t.Fatalf("%s leaked note data to a runner: %s", path, response.Body.String())
		}
	}
	ownerChanges := performJSONRequest(t, handler, http.MethodGet, "/api/v1/changes?after=0", owner.Token, nil)
	if !strings.Contains(ownerChanges.Body.String(), "note_id") {
		t.Fatal("the owner lost note events in the change feed")
	}
}

func TestNotesListParsesIncludeArchivedLikeABoolean(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t)
	owner := bootstrapOwner(t, handler)
	createResponse := performJSONRequest(t, handler, http.MethodPost, "/api/v1/notes", owner.Token, map[string]any{
		"document": "Alt", "client_request_id": requestID(t),
	})
	var note state.Note
	decodeResponse(t, createResponse, &note)
	performJSONRequest(t, handler, http.MethodPatch, "/api/v1/notes/"+note.ID, owner.Token, map[string]any{
		"archived": true, "expected_revision": 1, "client_request_id": requestID(t),
	})
	for _, value := range []string{"1", "TRUE", "true"} {
		response := performJSONRequest(t, handler, http.MethodGet, "/api/v1/notes?include_archived="+value, owner.Token, nil)
		if !strings.Contains(response.Body.String(), note.ID) {
			t.Fatalf("include_archived=%s hid the archived note", value)
		}
	}
	if response := performJSONRequest(t, handler, http.MethodGet, "/api/v1/notes?include_archived=vielleicht", owner.Token, nil); response.Code != http.StatusBadRequest {
		t.Fatalf("invalid include_archived status = %d", response.Code)
	}
	if response := performJSONRequest(t, handler, http.MethodPatch, "/api/v1/notes/"+note.ID, owner.Token, map[string]any{
		"document": "x", "client_request_id": requestID(t),
	}); response.Code != http.StatusBadRequest {
		t.Fatalf("missing expected_revision status = %d, want 400", response.Code)
	}
}
