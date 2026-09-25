package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/nicremo/state/internal/notesai"
	"github.com/nicremo/state/internal/notesai/fakeopenrouter"
	"github.com/nicremo/state/internal/state"
)

var testJPEG = append([]byte{0xFF, 0xD8, 0xFF, 0xE0}, bytes.Repeat([]byte("j"), 200)...)

func newAITestHandler(t *testing.T, key string) (http.Handler, *notesai.Runtime, *fakeopenrouter.Server) {
	t.Helper()
	fake := fakeopenrouter.New()
	t.Cleanup(fake.Close)
	repository, authManager, pushService := newTestBackend(t)
	gateway := notesai.NewGateway(notesai.NewClient(fake.BaseURL(), key, nil), state.DefaultNoteMediaPolicy())
	media, err := notesai.NewMediaStore(filepath.Join(t.TempDir(), "media"))
	if err != nil {
		t.Fatal(err)
	}
	service := state.NewService(repository, state.WithNoteAI(gateway.Configured, gateway.MediaPolicy))
	runtime := &notesai.Runtime{Gateway: gateway, Store: media, Worker: notesai.NewWorker(service, gateway, media, nil)}
	return NewHandler(Config{Auth: authManager, State: service, Push: pushService, Version: "test", NotesAI: runtime}), runtime, fake
}

func upload(t *testing.T, handler http.Handler, token string, noteID string, content []byte, mime string, ordinal int, hash string) *httptest.ResponseRecorder {
	t.Helper()
	if hash == "" {
		sum := sha256.Sum256(content)
		hash = hex.EncodeToString(sum[:])
	}
	request := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/notes/"+noteID+"/attachments", bytes.NewReader(content))
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", mime)
	request.Header.Set("X-State-Client-Request-Id", requestID(t))
	request.Header.Set("X-State-Ordinal", strconv.Itoa(ordinal))
	request.Header.Set("X-State-Content-SHA256", hash)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func TestPhotoNoteUploadProcessAndDownload(t *testing.T) {
	t.Parallel()
	handler, runtime, fake := newAITestHandler(t, "sk-or-test")
	owner := bootstrapOwner(t, handler)

	settings := performJSONRequest(t, handler, http.MethodPatch, "/api/v1/notes-ai/settings", owner.Token, map[string]any{"consent": true})
	if settings.Code != http.StatusOK || !strings.Contains(settings.Body.String(), `"key_configured":true`) || strings.Contains(settings.Body.String(), "sk-or-test") {
		t.Fatalf("settings = %d %s", settings.Code, settings.Body.String())
	}
	capabilities := performJSONRequest(t, handler, http.MethodGet, "/api/v1/note-capabilities", owner.Token, nil)
	var caps notesai.Capabilities
	decodeResponse(t, capabilities, &caps)
	if !caps.AIAvailable || !caps.Vision.Available || caps.Vision.MaxImages != 10 || !caps.Audio.Available {
		t.Fatalf("capabilities = %+v", caps)
	}

	created := performJSONRequest(t, handler, http.MethodPost, "/api/v1/notes", owner.Token, map[string]any{"capture": "image", "client_request_id": requestID(t)})
	var note state.NoteView
	decodeResponse(t, created, &note)
	if created.Code != http.StatusCreated || note.Capture != state.NoteCaptureImage || note.Processing.Status != state.NoteProcessingIdle {
		t.Fatalf("create = %d %s", created.Code, created.Body.String())
	}
	uploaded := upload(t, handler, owner.Token, note.ID, testJPEG, "image/jpeg", 0, "")
	if uploaded.Code != http.StatusCreated {
		t.Fatalf("upload = %d %s", uploaded.Code, uploaded.Body.String())
	}
	decodeResponse(t, uploaded, &note)
	if len(note.Attachments) != 1 || note.Attachments[0].ByteSize != int64(len(testJPEG)) {
		t.Fatalf("attachments = %+v", note.Attachments)
	}

	fake.ScriptChat(fakeopenrouter.ToolCall("s", "submit_result", map[string]any{
		"title": "Seite aus dem Buch", "summary": "Handschrift", "document": "## Seite\n- [ ] Brot",
		"image_texts": []map[string]any{{"index": 1, "text": "Brot"}},
	}, 0.001))
	processing := performJSONRequest(t, handler, http.MethodPost, "/api/v1/notes/"+note.ID+"/processing", owner.Token, map[string]any{"client_request_id": requestID(t)})
	if processing.Code != http.StatusAccepted || !strings.Contains(processing.Body.String(), `"status":"queued"`) {
		t.Fatalf("processing = %d %s", processing.Code, processing.Body.String())
	}
	if processed, err := runtime.Worker.RunOnce(context.Background()); err != nil || !processed {
		t.Fatalf("worker = %v %v", processed, err)
	}
	loaded := performJSONRequest(t, handler, http.MethodGet, "/api/v1/notes/"+note.ID, owner.Token, nil)
	decodeResponse(t, loaded, &note)
	if note.Title != "Seite aus dem Buch" || note.TitleSource != state.NoteFieldSourceAI || note.Attachments[0].DerivedText != "Brot" || !strings.Contains(note.Document, "Brot") {
		t.Fatalf("processed note = %+v", note)
	}

	download := httptest.NewRecorder()
	request := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/notes/"+note.ID+"/attachments/"+note.Attachments[0].ID, nil)
	request.Header.Set("Authorization", "Bearer "+owner.Token)
	handler.ServeHTTP(download, request)
	body, _ := io.ReadAll(download.Body)
	if download.Code != http.StatusOK || !bytes.Equal(body, testJPEG) || download.Header().Get("Content-Type") != "image/jpeg" {
		t.Fatalf("download = %d %s", download.Code, download.Header().Get("Content-Type"))
	}

	search := performJSONRequest(t, handler, http.MethodGet, "/api/v1/notes?q=Handschrift", owner.Token, nil)
	if !strings.Contains(search.Body.String(), note.ID) {
		t.Fatal("AI summary is not searchable")
	}
}

func TestUploadsAreValidated(t *testing.T) {
	t.Parallel()
	handler, _, _ := newAITestHandler(t, "sk-or-test")
	owner := bootstrapOwner(t, handler)
	runner := pairActor(t, handler, owner.Token, map[string]any{"kind": "runner", "display_name": "Mac"})
	created := performJSONRequest(t, handler, http.MethodPost, "/api/v1/notes", owner.Token, map[string]any{"capture": "image", "client_request_id": requestID(t)})
	var note state.NoteView
	decodeResponse(t, created, &note)

	cases := []struct {
		name    string
		token   string
		content []byte
		mime    string
		hash    string
		status  int
	}{
		{"wrong hash", owner.Token, testJPEG, "image/jpeg", strings.Repeat("0", 64), http.StatusBadRequest},
		{"wrong type", owner.Token, []byte("<script>"), "image/jpeg", "", http.StatusBadRequest},
		{"unsupported", owner.Token, testJPEG, "application/pdf", "", http.StatusBadRequest},
		{"too large", owner.Token, append(testJPEG, bytes.Repeat([]byte("x"), 9<<20)...), "image/jpeg", "", http.StatusBadRequest},
		{"runner", runner.Token, testJPEG, "image/jpeg", "", http.StatusForbidden},
	}
	for _, testCase := range cases {
		if response := upload(t, handler, testCase.token, note.ID, testCase.content, testCase.mime, 0, testCase.hash); response.Code != testCase.status {
			t.Errorf("%s: status %d, want %d (%s)", testCase.name, response.Code, testCase.status, response.Body.String())
		}
	}
	loaded := performJSONRequest(t, handler, http.MethodGet, "/api/v1/notes/"+note.ID, owner.Token, nil)
	decodeResponse(t, loaded, &note)
	if len(note.Attachments) != 0 {
		t.Fatalf("rejected upload attached: %+v", note.Attachments)
	}
	if response := performJSONRequest(t, handler, http.MethodGet, "/api/v1/note-capabilities", runner.Token, nil); response.Code != http.StatusForbidden {
		t.Fatalf("runner capabilities status = %d", response.Code)
	}
}

func TestWithoutKeyNotesStillWorkAndSayWhy(t *testing.T) {
	t.Parallel()
	handler, _, fake := newAITestHandler(t, "")
	owner := bootstrapOwner(t, handler)
	var caps notesai.Capabilities
	decodeResponse(t, performJSONRequest(t, handler, http.MethodGet, "/api/v1/note-capabilities", owner.Token, nil), &caps)
	if caps.AIAvailable || caps.Reason != notesai.ReasonNotConfigured || caps.Vision.MaxImages != 0 {
		t.Fatalf("capabilities = %+v", caps)
	}
	created := performJSONRequest(t, handler, http.MethodPost, "/api/v1/notes", owner.Token, map[string]any{"document": "Einfach eine Notiz", "client_request_id": requestID(t)})
	var note state.NoteView
	decodeResponse(t, created, &note)
	processing := performJSONRequest(t, handler, http.MethodPost, "/api/v1/notes/"+note.ID+"/processing", owner.Token, map[string]any{"client_request_id": requestID(t)})
	if processing.Code != http.StatusAccepted || !strings.Contains(processing.Body.String(), `"status":"not_configured"`) {
		t.Fatalf("processing = %d %s", processing.Code, processing.Body.String())
	}
	if len(fake.Requests("/chat/completions")) != 0 {
		t.Fatal("OpenRouter called without a key")
	}
}

func TestHarnessReadsButCannotChangeAISettingsOrAcceptProposals(t *testing.T) {
	t.Parallel()
	handler, runtime, fake := newAITestHandler(t, "sk-or-test")
	owner := bootstrapOwner(t, handler)
	harness := pairActor(t, handler, owner.Token, map[string]any{"harness": "codex", "display_name": "Codex", "device_name": "Mac"})

	if response := performJSONRequest(t, handler, http.MethodGet, "/api/v1/notes-ai/settings", harness.Token, nil); response.Code != http.StatusOK {
		t.Fatalf("harness read = %d", response.Code)
	}
	if response := performJSONRequest(t, handler, http.MethodPatch, "/api/v1/notes-ai/settings", harness.Token, map[string]any{"monthly_limit_usd": 500}); response.Code != http.StatusForbidden {
		t.Fatalf("harness write = %d", response.Code)
	}
	performJSONRequest(t, handler, http.MethodPatch, "/api/v1/notes-ai/settings", owner.Token, map[string]any{"consent": true})

	created := performJSONRequest(t, handler, http.MethodPost, "/api/v1/notes", harness.Token, map[string]any{"document": "Am 2. Oktober Report an Karla", "client_request_id": requestID(t)})
	var note state.NoteView
	decodeResponse(t, created, &note)
	fake.ScriptChat(
		fakeopenrouter.ToolCall("p", "propose_reminder", map[string]any{"title": "Report an Karla", "local_date": "2026-10-02", "local_time": "09:00", "reason": "Am 2. Oktober Report an Karla"}, 0.001),
		fakeopenrouter.ToolCall("s", "submit_result", map[string]any{"title": "Report", "summary": "Report an Karla"}, 0.001),
	)
	performJSONRequest(t, handler, http.MethodPost, "/api/v1/notes/"+note.ID+"/processing", harness.Token, map[string]any{"client_request_id": requestID(t)})
	if _, err := runtime.Worker.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	decodeResponse(t, performJSONRequest(t, handler, http.MethodGet, "/api/v1/notes/"+note.ID, owner.Token, nil), &note)
	if len(note.Proposals) != 1 {
		t.Fatalf("proposals = %+v", note.Proposals)
	}
	acceptPath := "/api/v1/notes/" + note.ID + "/reminder-proposals/" + note.Proposals[0].ID + "/accept"
	body := map[string]any{"time_zone": "Europe/Berlin", "client_request_id": requestID(t)}
	if response := performJSONRequest(t, handler, http.MethodPost, acceptPath, harness.Token, body); response.Code != http.StatusForbidden {
		t.Fatalf("harness accept = %d", response.Code)
	}
	accepted := performJSONRequest(t, handler, http.MethodPost, acceptPath, owner.Token, body)
	var reminder state.Reminder
	decodeResponse(t, accepted, &reminder)
	if accepted.Code != http.StatusCreated || reminder.Title != "Report an Karla" || reminder.Schedule == nil || reminder.Schedule.LocalDate != "2026-10-02" {
		t.Fatalf("accept = %d %s", accepted.Code, accepted.Body.String())
	}
}
