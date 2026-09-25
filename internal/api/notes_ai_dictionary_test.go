package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestDictionaryOwnerAndDevicesWriteAgentsRead(t *testing.T) {
	t.Parallel()
	handler, _, _ := newKeyTestHandler(t)
	owner := bootstrapOwner(t, handler)
	harness := pairActor(t, handler, owner.Token, map[string]any{"harness": "codex", "display_name": "Codex", "device_name": "Mac"})
	runner := pairActor(t, handler, owner.Token, map[string]any{"kind": "runner", "display_name": "Mac"})
	body := map[string]any{
		"words":       []string{"Supabase", "BLUNATECH"},
		"corrections": []map[string]any{{"from": "ZEVDISK", "to": "sevDesk", "mode": "always"}, {"from": "Note", "to": "Node", "mode": "context"}},
	}

	if response := performJSONRequest(t, handler, http.MethodPut, "/api/v1/notes-ai/dictionary", harness.Token, body); response.Code != http.StatusForbidden {
		t.Fatalf("agent changed the dictionary: %d", response.Code)
	}
	if response := performJSONRequest(t, handler, http.MethodGet, "/api/v1/notes-ai/dictionary", runner.Token, nil); response.Code != http.StatusForbidden {
		t.Fatalf("runner read the dictionary: %d", response.Code)
	}
	response := performJSONRequest(t, handler, http.MethodPut, "/api/v1/notes-ai/dictionary", owner.Token, body)
	if response.Code != http.StatusOK {
		t.Fatalf("owner save = %d %s", response.Code, response.Body.String())
	}
	var saved struct {
		Words       []string `json:"words"`
		Corrections []struct {
			From string `json:"from"`
			Mode string `json:"mode"`
		} `json:"corrections"`
		Revision int64 `json:"revision"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &saved); err != nil || saved.Revision != 1 || len(saved.Words) != 2 || saved.Corrections[1].Mode != "context" {
		t.Fatalf("saved = %+v, %v", saved, err)
	}

	read := performJSONRequest(t, handler, http.MethodGet, "/api/v1/notes-ai/dictionary", harness.Token, nil)
	if read.Code != http.StatusOK || !strings.Contains(read.Body.String(), "sevDesk") {
		t.Fatalf("agent read = %d %s", read.Code, read.Body.String())
	}

	body["expected_revision"] = 0
	if response := performJSONRequest(t, handler, http.MethodPut, "/api/v1/notes-ai/dictionary", owner.Token, body); response.Code != http.StatusConflict {
		t.Fatalf("stale save = %d", response.Code)
	}
	body["expected_revision"] = 1
	body["corrections"] = []map[string]any{{"from": "A", "to": "B", "mode": "sometimes"}}
	if response := performJSONRequest(t, handler, http.MethodPut, "/api/v1/notes-ai/dictionary", owner.Token, body); response.Code != http.StatusBadRequest {
		t.Fatalf("invalid save = %d", response.Code)
	}
	history := performJSONRequest(t, handler, http.MethodGet, "/api/v1/changes?after=0&limit=100", owner.Token, nil)
	if !strings.Contains(history.Body.String(), "notes_ai.dictionary_updated") {
		t.Fatal("dictionary change is not audited")
	}
}
