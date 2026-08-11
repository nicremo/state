package api

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	stateauth "github.com/nicremo/state/internal/auth"
	"github.com/nicremo/state/internal/state"
	"github.com/nicremo/state/internal/store"
	"github.com/pocketbase/pocketbase"
)

func TestReminderAPIEndToEnd(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t)
	owner := bootstrapOwner(t, handler)
	pairingResponse := performJSONRequest(t, handler, http.MethodPost, "/api/v1/pairing/codes", owner.Token, map[string]any{
		"harness":      "claude-code",
		"display_name": "Claude Code",
		"device_name":  "MacBook",
	})
	if pairingResponse.Code != http.StatusCreated {
		t.Fatalf("pairing status = %d, body = %s", pairingResponse.Code, pairingResponse.Body.String())
	}
	var pairing stateauth.PairingCode
	decodeResponse(t, pairingResponse, &pairing)
	exchangeResponse := performJSONRequest(t, handler, http.MethodPost, "/api/v1/pairing/exchange", "", map[string]any{
		"code": pairing.Code,
	})
	if exchangeResponse.Code != http.StatusCreated {
		t.Fatalf("exchange status = %d, body = %s", exchangeResponse.Code, exchangeResponse.Body.String())
	}
	var harness stateauth.Credential
	decodeResponse(t, exchangeResponse, &harness)

	createResponse := performJSONRequest(t, handler, http.MethodPost, "/api/v1/reminders", harness.Token, map[string]any{
		"title":             "Prepare the review",
		"description":       "Use the latest metrics.",
		"client_request_id": "01989de4-8533-7c6d-8674-c71dbdbcbf71",
		"source_excerpt":    "Remind me on 17 August at 9",
		"schedule": map[string]any{
			"local_date": "2026-08-17",
			"local_time": "09:00",
			"time_zone":  "Europe/Copenhagen",
			"mode":       "floating",
		},
	})
	if createResponse.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body = %s", createResponse.Code, createResponse.Body.String())
	}
	var reminder state.Reminder
	decodeResponse(t, createResponse, &reminder)

	detailResponse := performJSONRequest(t, handler, http.MethodGet, "/api/v1/reminders/"+reminder.ID, harness.Token, nil)
	if detailResponse.Code != http.StatusOK {
		t.Fatalf("detail status = %d, body = %s", detailResponse.Code, detailResponse.Body.String())
	}
	var detail ReminderDetail
	decodeResponse(t, detailResponse, &detail)
	if detail.Reminder.ID != reminder.ID || len(detail.History) != 1 {
		t.Fatalf("unexpected reminder detail: %#v", detail)
	}
	if detail.History[0].Actor.Harness != "claude-code" || detail.History[0].Actor.DeviceName != "MacBook" {
		t.Fatalf("unexpected history actor: %#v", detail.History[0].Actor)
	}
	if detail.History[0].SourceExcerpt != "Remind me on 17 August at 9" {
		t.Fatalf("source excerpt = %q", detail.History[0].SourceExcerpt)
	}
}

func TestReminderAPIMapsRevisionConflictAndAgentArchive(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t)
	owner := bootstrapOwner(t, handler)
	pairingResponse := performJSONRequest(t, handler, http.MethodPost, "/api/v1/pairing/codes", owner.Token, map[string]any{
		"harness":      "codex",
		"display_name": "Codex",
		"device_name":  "MacBook",
	})
	var pairing stateauth.PairingCode
	decodeResponse(t, pairingResponse, &pairing)
	exchangeResponse := performJSONRequest(t, handler, http.MethodPost, "/api/v1/pairing/exchange", "", map[string]any{"code": pairing.Code})
	var harness stateauth.Credential
	decodeResponse(t, exchangeResponse, &harness)
	createResponse := performJSONRequest(t, handler, http.MethodPost, "/api/v1/reminders", harness.Token, map[string]any{
		"title":             "Conflict target",
		"client_request_id": "01989de4-8533-7d9a-adbe-60994225b786",
	})
	var reminder state.Reminder
	decodeResponse(t, createResponse, &reminder)

	conflictResponse := performJSONRequest(t, handler, http.MethodPatch, "/api/v1/reminders/"+reminder.ID, harness.Token, map[string]any{
		"title":             "Stale update",
		"expected_revision": 0,
		"client_request_id": "01989de4-8533-72ee-9a61-d5e43db2b979",
	})
	if conflictResponse.Code != http.StatusConflict {
		t.Fatalf("conflict status = %d, body = %s", conflictResponse.Code, conflictResponse.Body.String())
	}
	var conflict ErrorResponse
	decodeResponse(t, conflictResponse, &conflict)
	if conflict.Code != "revision_conflict" {
		t.Fatalf("conflict code = %q", conflict.Code)
	}

	archiveResponse := performJSONRequest(t, handler, http.MethodPatch, "/api/v1/reminders/"+reminder.ID, harness.Token, map[string]any{
		"archived":          true,
		"expected_revision": 1,
		"client_request_id": "01989de4-8533-7102-9996-03d900c8b96b",
	})
	if archiveResponse.Code != http.StatusForbidden {
		t.Fatalf("archive status = %d, body = %s", archiveResponse.Code, archiveResponse.Body.String())
	}
}

func TestCommentAPIAddsContextToReminderDetail(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t)
	owner := bootstrapOwner(t, handler)
	createResponse := performJSONRequest(t, handler, http.MethodPost, "/api/v1/reminders", owner.Token, map[string]any{
		"title":             "Comment target",
		"client_request_id": "01989e82-adb5-7ce3-aaf1-a9b29ddc2a33",
	})
	var reminder state.Reminder
	decodeResponse(t, createResponse, &reminder)
	commentResponse := performJSONRequest(t, handler, http.MethodPost, "/api/v1/reminders/"+reminder.ID+"/comments", owner.Token, map[string]any{
		"body":              "This came from the iPhone.",
		"client_request_id": "01989e82-adb5-7f73-8245-e07e516b0cc9",
	})
	if commentResponse.Code != http.StatusCreated {
		t.Fatalf("comment status = %d, body = %s", commentResponse.Code, commentResponse.Body.String())
	}
	var comment state.Comment
	decodeResponse(t, commentResponse, &comment)
	if comment.Actor.Kind != state.ActorKindOwner {
		t.Fatalf("comment actor = %#v", comment.Actor)
	}

	detailResponse := performJSONRequest(t, handler, http.MethodGet, "/api/v1/reminders/"+reminder.ID, owner.Token, nil)
	var detail ReminderDetail
	decodeResponse(t, detailResponse, &detail)
	if len(detail.Comments) != 1 || detail.Comments[0].Body != "This came from the iPhone." {
		t.Fatalf("detail comments = %#v", detail.Comments)
	}
	if len(detail.History) != 2 || detail.History[1].Action != state.AuditActionCommentAdded {
		t.Fatalf("detail history = %#v", detail.History)
	}
}

func TestOccurrenceAPICompletesScheduledReminder(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t)
	owner := bootstrapOwner(t, handler)
	createResponse := performJSONRequest(t, handler, http.MethodPost, "/api/v1/reminders", owner.Token, map[string]any{
		"title":             "Scheduled API reminder",
		"client_request_id": "01989ee8-3d9d-706d-9de7-472a46242086",
		"schedule": map[string]any{
			"local_date": "2026-08-17",
			"local_time": "09:00",
			"time_zone":  "Europe/Copenhagen",
			"mode":       "floating",
		},
	})
	var reminder state.Reminder
	decodeResponse(t, createResponse, &reminder)
	detailResponse := performJSONRequest(t, handler, http.MethodGet, "/api/v1/reminders/"+reminder.ID, owner.Token, nil)
	var detail ReminderDetail
	decodeResponse(t, detailResponse, &detail)
	if len(detail.Occurrences) != 1 {
		t.Fatalf("detail occurrences = %#v", detail.Occurrences)
	}
	completeResponse := performJSONRequest(t, handler, http.MethodPost, "/api/v1/occurrences/"+detail.Occurrences[0].ID+"/complete", owner.Token, map[string]any{
		"expected_revision": 1,
		"client_request_id": "01989ee8-3d9d-7b5d-a2ec-cf12c571aa29",
	})
	if completeResponse.Code != http.StatusOK {
		t.Fatalf("complete status = %d, body = %s", completeResponse.Code, completeResponse.Body.String())
	}
	var completed state.Occurrence
	decodeResponse(t, completeResponse, &completed)
	if completed.Status != state.OccurrenceStatusCompleted || completed.Revision != 2 {
		t.Fatalf("completed occurrence = %#v", completed)
	}
}

func TestHealthAndVersionEndpointsArePublic(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t)
	for _, path := range []string{"/health/live", "/health/ready", "/version"} {
		response := performJSONRequest(t, handler, http.MethodGet, path, "", nil)
		if response.Code != http.StatusOK {
			t.Fatalf("GET %s status = %d, body = %s", path, response.Code, response.Body.String())
		}
	}
}

func TestListSearchChangesAndBriefingEndpoints(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t)
	owner := bootstrapOwner(t, handler)
	for index, title := range []string{"Quarterly metrics", "Dentist appointment"} {
		response := performJSONRequest(t, handler, http.MethodPost, "/api/v1/reminders", owner.Token, map[string]any{
			"title":             title,
			"description":       "Context for " + title,
			"client_request_id": []string{"01989e39-3ba8-7e61-a572-6306a8dc00ed", "01989e39-3ba8-7765-9b6f-15aff25c5e09"}[index],
		})
		if response.Code != http.StatusCreated {
			t.Fatalf("create %d status = %d, body = %s", index, response.Code, response.Body.String())
		}
	}

	listResponse := performJSONRequest(t, handler, http.MethodGet, "/api/v1/reminders", owner.Token, nil)
	if listResponse.Code != http.StatusOK {
		t.Fatalf("list status = %d, body = %s", listResponse.Code, listResponse.Body.String())
	}
	var listResult struct {
		Reminders []state.Reminder `json:"reminders"`
	}
	decodeResponse(t, listResponse, &listResult)
	if len(listResult.Reminders) != 2 {
		t.Fatalf("list reminder count = %d, want 2", len(listResult.Reminders))
	}

	searchResponse := performJSONRequest(t, handler, http.MethodGet, "/api/v1/reminders?q=quarterly", owner.Token, nil)
	var searchResult struct {
		Reminders []state.Reminder `json:"reminders"`
	}
	decodeResponse(t, searchResponse, &searchResult)
	if len(searchResult.Reminders) != 1 || searchResult.Reminders[0].Title != "Quarterly metrics" {
		t.Fatalf("search reminders = %#v", searchResult.Reminders)
	}

	changesResponse := performJSONRequest(t, handler, http.MethodGet, "/api/v1/changes?after=0&limit=20", owner.Token, nil)
	if changesResponse.Code != http.StatusOK {
		t.Fatalf("changes status = %d, body = %s", changesResponse.Code, changesResponse.Body.String())
	}
	var changesResult struct {
		Changes []state.Change `json:"changes"`
		Cursor  int64          `json:"cursor"`
	}
	decodeResponse(t, changesResponse, &changesResult)
	if len(changesResult.Changes) != 2 || changesResult.Cursor != 2 {
		t.Fatalf("changes = %#v, cursor = %d", changesResult.Changes, changesResult.Cursor)
	}

	briefingResponse := performJSONRequest(t, handler, http.MethodGet, "/api/v1/briefing?after=0", owner.Token, nil)
	if briefingResponse.Code != http.StatusOK {
		t.Fatalf("briefing status = %d, body = %s", briefingResponse.Code, briefingResponse.Body.String())
	}
	var briefing state.Briefing
	decodeResponse(t, briefingResponse, &briefing)
	if len(briefing.Reminders) != 2 || briefing.Cursor != 2 {
		t.Fatalf("briefing = %#v", briefing)
	}
}

func TestCredentialAndActorManagementAPI(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t)
	owner := bootstrapOwner(t, handler)
	pairingResponse := performJSONRequest(t, handler, http.MethodPost, "/api/v1/pairing/codes", owner.Token, map[string]any{
		"harness":      "codex",
		"display_name": "Codex",
		"device_name":  "MacBook",
	})
	var pairing stateauth.PairingCode
	decodeResponse(t, pairingResponse, &pairing)
	exchangeResponse := performJSONRequest(t, handler, http.MethodPost, "/api/v1/pairing/exchange", "", map[string]any{"code": pairing.Code})
	var harness stateauth.Credential
	decodeResponse(t, exchangeResponse, &harness)

	agentsResponse := performJSONRequest(t, handler, http.MethodGet, "/api/v1/agents", owner.Token, nil)
	if agentsResponse.Code != http.StatusOK {
		t.Fatalf("agents status = %d, body = %s", agentsResponse.Code, agentsResponse.Body.String())
	}
	var actors struct {
		Actors []stateauth.ActorRecord `json:"actors"`
	}
	decodeResponse(t, agentsResponse, &actors)
	if len(actors.Actors) != 1 || actors.Actors[0].Actor.ID != harness.Actor.ID {
		t.Fatalf("agents = %#v", actors.Actors)
	}

	rotateResponse := performJSONRequest(t, handler, http.MethodPost, "/api/v1/credentials/rotate", harness.Token, map[string]any{})
	if rotateResponse.Code != http.StatusOK {
		t.Fatalf("rotate status = %d, body = %s", rotateResponse.Code, rotateResponse.Body.String())
	}
	var rotated stateauth.Credential
	decodeResponse(t, rotateResponse, &rotated)
	if rotated.Token == "" || rotated.Token == harness.Token {
		t.Fatalf("rotated token was not replaced")
	}
	oldResponse := performJSONRequest(t, handler, http.MethodGet, "/api/v1/briefing", harness.Token, nil)
	if oldResponse.Code != http.StatusUnauthorized {
		t.Fatalf("old token status = %d", oldResponse.Code)
	}
	revokeResponse := performJSONRequest(t, handler, http.MethodPost, "/api/v1/credentials/revoke", rotated.Token, map[string]any{})
	if revokeResponse.Code != http.StatusNoContent {
		t.Fatalf("revoke status = %d, body = %s", revokeResponse.Code, revokeResponse.Body.String())
	}
	revokedResponse := performJSONRequest(t, handler, http.MethodGet, "/api/v1/briefing", rotated.Token, nil)
	if revokedResponse.Code != http.StatusUnauthorized {
		t.Fatalf("revoked token status = %d", revokedResponse.Code)
	}
}

func newTestHandler(t *testing.T) http.Handler {
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
		seed[index] = byte(index + 11)
	}
	repository, err := store.NewPocketBaseRepository(app, ed25519.NewKeyFromSeed(seed))
	if err != nil {
		t.Fatalf("NewPocketBaseRepository() error = %v", err)
	}
	authManager, err := stateauth.NewManager(app, "bootstrap-secret")
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	return NewHandler(Config{
		Auth:    authManager,
		State:   state.NewService(repository),
		Version: "test-version",
	})
}

func bootstrapOwner(t *testing.T, handler http.Handler) stateauth.Credential {
	t.Helper()

	body, err := json.Marshal(map[string]any{
		"display_name": "Fabian",
		"device_name":  "iPhone",
	})
	if err != nil {
		t.Fatalf("encode bootstrap request: %v", err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/pairing/owner", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-State-Bootstrap-Token", "bootstrap-secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("bootstrap status = %d, body = %s", response.Code, response.Body.String())
	}
	var credential stateauth.Credential
	decodeResponse(t, response, &credential)
	return credential
}

func performJSONRequest(t *testing.T, handler http.Handler, method string, path string, token string, input any) *httptest.ResponseRecorder {
	t.Helper()

	var body bytes.Buffer
	if input != nil {
		if err := json.NewEncoder(&body).Encode(input); err != nil {
			t.Fatalf("encode request: %v", err)
		}
	}
	request := httptest.NewRequestWithContext(context.Background(), method, path, &body)
	request.Header.Set("Content-Type", "application/json")
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func decodeResponse(t *testing.T, response *httptest.ResponseRecorder, output any) {
	t.Helper()
	if err := json.Unmarshal(response.Body.Bytes(), output); err != nil {
		t.Fatalf("decode response %q: %v", response.Body.String(), err)
	}
}

func TestErrorMappingCoversDomainErrors(t *testing.T) {
	t.Parallel()

	cases := []struct {
		err    error
		status int
		code   string
	}{
		{state.ErrNotFound, http.StatusNotFound, "not_found"},
		{state.ErrRevisionConflict, http.StatusConflict, "revision_conflict"},
		{state.ErrForbidden, http.StatusForbidden, "forbidden"},
		{state.ErrInvalidInput, http.StatusBadRequest, "invalid_input"},
		{stateauth.ErrInvalidCredential, http.StatusUnauthorized, "unauthorized"},
	}
	for _, testCase := range cases {
		status, code := mapError(testCase.err)
		if status != testCase.status || code != testCase.code {
			t.Errorf("mapError(%v) = %d, %q, want %d, %q", testCase.err, status, code, testCase.status, testCase.code)
		}
	}
	status, code := mapError(errors.New("unexpected"))
	if status != http.StatusInternalServerError || code != "internal_error" {
		t.Fatalf("mapError(unexpected) = %d, %q", status, code)
	}
}
