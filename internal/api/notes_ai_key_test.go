package api

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nicremo/state/internal/notesai"
	"github.com/nicremo/state/internal/notesai/fakeopenrouter"
	"github.com/nicremo/state/internal/state"
)

func newKeyTestHandler(t *testing.T) (http.Handler, *notesai.Runtime, string) {
	t.Helper()
	fake := fakeopenrouter.New()
	t.Cleanup(fake.Close)
	repository, authManager, pushService := newTestBackend(t)
	keyPath := filepath.Join(t.TempDir(), "state_secrets", "openrouter.key")
	gateway := notesai.NewGateway(notesai.NewClient(fake.BaseURL(), "", nil), state.DefaultNoteMediaPolicy())
	media, err := notesai.NewMediaStore(filepath.Join(t.TempDir(), "media"))
	if err != nil {
		t.Fatal(err)
	}
	service := state.NewService(repository, state.WithNoteAI(gateway.Configured, gateway.MediaPolicy))
	runtime := &notesai.Runtime{Gateway: gateway, Store: media, Worker: notesai.NewWorker(service, gateway, media, nil), KeyPath: keyPath}
	return NewHandler(Config{Auth: authManager, State: service, Push: pushService, Version: "test", NotesAI: runtime}), runtime, keyPath
}

func TestOwnerSetsAndRemovesTheOpenRouterKeyInTheApp(t *testing.T) {
	t.Parallel()
	handler, runtime, keyPath := newKeyTestHandler(t)
	owner := bootstrapOwner(t, handler)
	harness := pairActor(t, handler, owner.Token, map[string]any{"harness": "codex", "display_name": "Codex", "device_name": "Mac"})
	runner := pairActor(t, handler, owner.Token, map[string]any{"kind": "runner", "display_name": "Mac"})
	const key = "sk-or-v1-0123456789abcdef0123456789abcdef0123456789abcdef0123456789ab"

	for _, token := range []string{harness.Token, runner.Token} {
		if response := performJSONRequest(t, handler, http.MethodPut, "/api/v1/notes-ai/key", token, map[string]any{"key": key}); response.Code != http.StatusForbidden {
			t.Fatalf("agent set the key: %d", response.Code)
		}
	}
	if response := performJSONRequest(t, handler, http.MethodPut, "/api/v1/notes-ai/key", owner.Token, map[string]any{"key": "not a key"}); response.Code != http.StatusBadRequest {
		t.Fatalf("malformed key status = %d", response.Code)
	}
	if response := performJSONRequest(t, handler, http.MethodPut, "/api/v1/notes-ai/key", owner.Token, map[string]any{"key": "sk-or-v1-bad0000000000000000000000000000000000000000000000000000000000"}); response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "invalid_key") {
		t.Fatalf("rejected key status = %d %s", response.Code, response.Body.String())
	}
	if runtime.Gateway.Configured() {
		t.Fatal("a rejected key was taken")
	}

	response := performJSONRequest(t, handler, http.MethodPut, "/api/v1/notes-ai/key", owner.Token, map[string]any{"key": key})
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"key_configured":true`) || strings.Contains(response.Body.String(), key) {
		t.Fatalf("set key = %d %s", response.Code, response.Body.String())
	}
	if !runtime.Gateway.Configured() {
		t.Fatal("key not active without a restart")
	}
	info, err := os.Stat(keyPath)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("key file = %v, %v", info, err)
	}
	stored, _ := os.ReadFile(keyPath)
	if strings.TrimSpace(string(stored)) != key {
		t.Fatal("stored key differs")
	}
	caps := performJSONRequest(t, handler, http.MethodGet, "/api/v1/note-capabilities", owner.Token, nil)
	if !strings.Contains(caps.Body.String(), `"ai_available":true`) {
		t.Fatalf("capabilities after key = %s", caps.Body.String())
	}
	history := performJSONRequest(t, handler, http.MethodGet, "/api/v1/changes?after=0&limit=100", owner.Token, nil)
	if strings.Contains(history.Body.String(), key) || !strings.Contains(history.Body.String(), "notes_ai.key_updated") {
		t.Fatal("key change is not audited, or the audit holds the key")
	}

	if response := performJSONRequest(t, handler, http.MethodDelete, "/api/v1/notes-ai/key", harness.Token, nil); response.Code != http.StatusForbidden {
		t.Fatalf("agent removed the key: %d", response.Code)
	}
	if response := performJSONRequest(t, handler, http.MethodDelete, "/api/v1/notes-ai/key", owner.Token, nil); response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"key_configured":false`) {
		t.Fatalf("remove key = %d %s", response.Code, response.Body.String())
	}
	if _, err := os.Stat(keyPath); !os.IsNotExist(err) || runtime.Gateway.Configured() {
		t.Fatal("key still present after removal")
	}
}
