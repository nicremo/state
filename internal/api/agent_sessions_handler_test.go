package api

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"
)

func TestAgentSessionEndpoints(t *testing.T) {
	t.Parallel()
	handler, _, _ := newKeyTestHandler(t)
	owner := bootstrapOwner(t, handler)
	harness := pairActor(t, handler, owner.Token, map[string]any{"harness": "codex", "display_name": "Codex", "device_name": "Mac"})
	runner := pairActor(t, handler, owner.Token, map[string]any{"kind": "runner", "display_name": "Mac"})

	decode := func(response interface{ Bytes() []byte }, target any) {
		t.Helper()
		if err := json.Unmarshal(response.Bytes(), target); err != nil {
			t.Fatalf("decode: %v", err)
		}
	}
	project := struct{ ID string }{}
	response := performJSONRequest(t, handler, http.MethodPost, "/api/v1/projects", owner.Token, map[string]any{"name": "karla-report", "client_request_id": uuid.NewString()})
	if response.Code != http.StatusCreated {
		t.Fatalf("project = %d %s", response.Code, response.Body.String())
	}
	decode(response.Body, &project)
	policy := struct{ ID string }{}
	response = performJSONRequest(t, handler, http.MethodPost, "/api/v1/policies", owner.Token, map[string]any{
		"name": "karla-claude", "project_id": project.ID, "adapter": "claude-code", "mode": "supervised",
		"allowed_capabilities": []string{"read_repository"}, "timeout_minutes": 30, "client_request_id": uuid.NewString(),
	})
	if response.Code != http.StatusCreated {
		t.Fatalf("policy = %d %s", response.Code, response.Body.String())
	}
	decode(response.Body, &policy)
	reminder := struct{ ID string }{}
	response = performJSONRequest(t, handler, http.MethodPost, "/api/v1/reminders", owner.Token, map[string]any{"title": "Karla-Monatsreport", "client_request_id": uuid.NewString()})
	if response.Code != http.StatusCreated {
		t.Fatalf("reminder = %d %s", response.Code, response.Body.String())
	}
	decode(response.Body, &reminder)

	start := map[string]any{"reminder_id": reminder.ID, "policy_id": policy.ID, "instruction": "Los", "client_request_id": uuid.NewString()}
	for _, token := range []string{harness.Token, runner.Token} {
		if response := performJSONRequest(t, handler, http.MethodPost, "/api/v1/agent-sessions", token, start); response.Code != http.StatusForbidden {
			t.Fatalf("agent or runner started a session: %d", response.Code)
		}
	}
	response = performJSONRequest(t, handler, http.MethodPost, "/api/v1/agent-sessions", owner.Token, start)
	if response.Code != http.StatusCreated {
		t.Fatalf("start = %d %s", response.Code, response.Body.String())
	}
	session := struct {
		ID     string `json:"id"`
		Status string `json:"status"`
		Turns  []struct {
			TurnKind string `json:"turn_kind"`
			Prompt   string `json:"prompt"`
		} `json:"turns"`
	}{}
	decode(response.Body, &session)
	if session.Status != "working" || len(session.Turns) != 1 || session.Turns[0].TurnKind != "message" || session.Turns[0].Prompt != "Los" {
		t.Fatalf("session = %+v", session)
	}

	message := map[string]any{"text": "Weiter", "client_request_id": uuid.NewString()}
	if response := performJSONRequest(t, handler, http.MethodPost, "/api/v1/agent-sessions/"+session.ID+"/messages", owner.Token, message); response.Code != http.StatusConflict {
		t.Fatalf("message while working = %d %s", response.Code, response.Body.String())
	}
	if response := performJSONRequest(t, handler, http.MethodPost, "/api/v1/agent-sessions/"+session.ID+"/messages", owner.Token, map[string]any{"text": "", "client_request_id": uuid.NewString()}); response.Code != http.StatusBadRequest {
		t.Fatalf("empty message = %d", response.Code)
	}
	if response := performJSONRequest(t, handler, http.MethodPost, "/api/v1/agent-sessions/"+session.ID+"/open", owner.Token, map[string]any{"client_request_id": uuid.NewString()}); response.Code != http.StatusConflict {
		t.Fatalf("open while working = %d", response.Code)
	}
	if response := performJSONRequest(t, handler, http.MethodPost, "/api/v1/agent-sessions/"+session.ID+"/close", owner.Token, map[string]any{"client_request_id": uuid.NewString()}); response.Code != http.StatusConflict {
		t.Fatalf("close while working = %d", response.Code)
	}
	list := performJSONRequest(t, handler, http.MethodGet, "/api/v1/agent-sessions", owner.Token, nil)
	sessions := struct {
		Sessions []struct{ ID string } `json:"sessions"`
	}{}
	decode(list.Body, &sessions)
	if list.Code != http.StatusOK || len(sessions.Sessions) != 1 {
		t.Fatalf("list = %d %s", list.Code, list.Body.String())
	}
	if response := performJSONRequest(t, handler, http.MethodGet, "/api/v1/agent-sessions", harness.Token, nil); response.Code != http.StatusForbidden {
		t.Fatalf("agent listed sessions: %d", response.Code)
	}
	if response := performJSONRequest(t, handler, http.MethodGet, "/api/v1/agent-sessions/"+session.ID, owner.Token, nil); response.Code != http.StatusOK {
		t.Fatalf("get = %d", response.Code)
	}
	if response := performJSONRequest(t, handler, http.MethodGet, "/api/v1/agent-sessions/"+uuid.NewString(), owner.Token, nil); response.Code != http.StatusNotFound {
		t.Fatalf("missing session = %d", response.Code)
	}
}
