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

	"github.com/google/uuid"
	stateauth "github.com/nicremo/state/internal/auth"
	statepush "github.com/nicremo/state/internal/push"
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

func TestOwnerExportIncludesArchivedReminderContext(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t)
	owner := bootstrapOwner(t, handler)
	createResponse := performJSONRequest(t, handler, http.MethodPost, "/api/v1/reminders", owner.Token, map[string]any{
		"title":             "Export target",
		"client_request_id": "0198a188-43d7-7465-89be-d72d98fe833e",
	})
	var reminder state.Reminder
	decodeResponse(t, createResponse, &reminder)
	commentResponse := performJSONRequest(t, handler, http.MethodPost, "/api/v1/reminders/"+reminder.ID+"/comments", owner.Token, map[string]any{
		"body":              "Retained export context",
		"client_request_id": "0198a188-43d7-7b12-9d95-818755fa22a9",
	})
	if commentResponse.Code != http.StatusCreated {
		t.Fatalf("comment status = %d, body = %s", commentResponse.Code, commentResponse.Body.String())
	}
	archiveResponse := performJSONRequest(t, handler, http.MethodPatch, "/api/v1/reminders/"+reminder.ID, owner.Token, map[string]any{
		"archived":          true,
		"expected_revision": 1,
		"client_request_id": "0198a188-43d7-77b1-8a29-8719e3fe99c9",
	})
	if archiveResponse.Code != http.StatusOK {
		t.Fatalf("archive status = %d, body = %s", archiveResponse.Code, archiveResponse.Body.String())
	}

	exportResponse := performJSONRequest(t, handler, http.MethodGet, "/api/v1/export", owner.Token, nil)
	if exportResponse.Code != http.StatusOK {
		t.Fatalf("export status = %d, body = %s", exportResponse.Code, exportResponse.Body.String())
	}
	var exported Export
	decodeResponse(t, exportResponse, &exported)
	if exported.APIVersion != "v1" || exported.Cursor != 3 || len(exported.Reminders) != 1 {
		t.Fatalf("export = %#v", exported)
	}
	detail := exported.Reminders[0]
	if !detail.Reminder.Archived || len(detail.Comments) != 1 || len(detail.History) != 3 {
		t.Fatalf("exported detail = %#v", detail)
	}

	pairingResponse := performJSONRequest(t, handler, http.MethodPost, "/api/v1/pairing/codes", owner.Token, map[string]any{
		"harness":      "opencode",
		"display_name": "OpenCode",
		"device_name":  "MacBook",
	})
	var pairing stateauth.PairingCode
	decodeResponse(t, pairingResponse, &pairing)
	exchangeResponse := performJSONRequest(t, handler, http.MethodPost, "/api/v1/pairing/exchange", "", map[string]any{"code": pairing.Code})
	var harness stateauth.Credential
	decodeResponse(t, exchangeResponse, &harness)
	forbiddenResponse := performJSONRequest(t, handler, http.MethodGet, "/api/v1/export", harness.Token, nil)
	if forbiddenResponse.Code != http.StatusForbidden {
		t.Fatalf("harness export status = %d, body = %s", forbiddenResponse.Code, forbiddenResponse.Body.String())
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

func TestDevicePushRegistrationAndConfirmationAPI(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t)
	owner := bootstrapOwner(t, handler)
	registerResponse := performJSONRequest(t, handler, http.MethodPut, "/api/v1/devices/push", owner.Token, map[string]any{
		"relay_url":     "https://relay.example.com",
		"route_id":      "0198a127-8780-724a-aa12-0ff815ba7789",
		"authorization": "route-capability",
		"public_key":    bytes.Repeat([]byte{0x42}, 32),
	})
	if registerResponse.Code != http.StatusOK {
		t.Fatalf("push registration status = %d, body = %s", registerResponse.Code, registerResponse.Body.String())
	}
	var route statepush.DeviceRoute
	decodeResponse(t, registerResponse, &route)
	if route.ActorID != owner.Actor.ID || route.RouteID == "" || route.Authorization != "" {
		t.Fatalf("push route = %#v", route)
	}

	createResponse := performJSONRequest(t, handler, http.MethodPost, "/api/v1/reminders", owner.Token, map[string]any{
		"title":             "Locally scheduled",
		"client_request_id": "0198a127-8780-7dde-87db-200a4ad36812",
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
	confirmResponse := performJSONRequest(t, handler, http.MethodPost, "/api/v1/devices/push/confirmations", owner.Token, map[string]any{
		"occurrence_ids": []string{detail.Occurrences[0].ID},
	})
	if confirmResponse.Code != http.StatusNoContent {
		t.Fatalf("confirmation status = %d, body = %s", confirmResponse.Code, confirmResponse.Body.String())
	}
	deleteResponse := performJSONRequest(t, handler, http.MethodDelete, "/api/v1/devices/push", owner.Token, nil)
	if deleteResponse.Code != http.StatusNoContent {
		t.Fatalf("delete push status = %d, body = %s", deleteResponse.Code, deleteResponse.Body.String())
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
	pushRepository, err := statepush.NewRepository(app, bytes.Repeat([]byte{0x73}, 32))
	if err != nil {
		t.Fatalf("NewRepository(push) error = %v", err)
	}
	return NewHandler(Config{
		Auth:    authManager,
		State:   state.NewService(repository),
		Push:    statepush.NewService(pushRepository, statepush.NewHTTPSender(nil)),
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
		{state.ErrNotClaimable, http.StatusConflict, "not_claimable"},
		{state.ErrRunStateConflict, http.StatusConflict, "run_state_conflict"},
		{state.ErrPolicyViolation, http.StatusForbidden, "policy_violation"},
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

// pairActor drives the real pairing flow over HTTP and returns the new
// actor's credential.
func pairActor(t *testing.T, handler http.Handler, ownerToken string, request map[string]any) stateauth.Credential {
	t.Helper()

	pairingResponse := performJSONRequest(t, handler, http.MethodPost, "/api/v1/pairing/codes", ownerToken, request)
	if pairingResponse.Code != http.StatusCreated {
		t.Fatalf("pairing status = %d, body = %s", pairingResponse.Code, pairingResponse.Body.String())
	}
	var pairing stateauth.PairingCode
	decodeResponse(t, pairingResponse, &pairing)
	exchangeResponse := performJSONRequest(t, handler, http.MethodPost, "/api/v1/pairing/exchange", "", map[string]any{"code": pairing.Code})
	if exchangeResponse.Code != http.StatusCreated {
		t.Fatalf("exchange status = %d, body = %s", exchangeResponse.Code, exchangeResponse.Body.String())
	}
	var credential stateauth.Credential
	decodeResponse(t, exchangeResponse, &credential)
	return credential
}

func mustCreateProjectAPI(t *testing.T, handler http.Handler, ownerToken string, name string) state.Project {
	t.Helper()

	response := performJSONRequest(t, handler, http.MethodPost, "/api/v1/projects", ownerToken, map[string]any{
		"name":              name,
		"description":       "Customer facing API",
		"root_path_hint":    "~/src/" + name,
		"client_request_id": requestID(t),
	})
	if response.Code != http.StatusCreated {
		t.Fatalf("create project status = %d, body = %s", response.Code, response.Body.String())
	}
	var project state.Project
	decodeResponse(t, response, &project)
	return project
}

func mustCreatePolicyAPI(t *testing.T, handler http.Handler, ownerToken string, projectID string) state.ExecutionPolicy {
	t.Helper()

	response := performJSONRequest(t, handler, http.MethodPost, "/api/v1/policies", ownerToken, map[string]any{
		"name":                            "nightly-review",
		"project_id":                      projectID,
		"adapter":                         "codex",
		"mode":                            "supervised",
		"allowed_capabilities":            []string{"read_repository", "run_tests"},
		"mark_occurrence_done_on_success": true,
		"notify_on_completion":            true,
		"notify_on_failure":               true,
		"timeout_minutes":                 30,
		"client_request_id":               requestID(t),
	})
	if response.Code != http.StatusCreated {
		t.Fatalf("create policy status = %d, body = %s", response.Code, response.Body.String())
	}
	var policy state.ExecutionPolicy
	decodeResponse(t, response, &policy)
	return policy
}

func mustRegisterRunnerAPI(t *testing.T, handler http.Handler, runnerToken string, projects []string) state.Runner {
	t.Helper()

	response := performJSONRequest(t, handler, http.MethodPost, "/api/v1/runner/registration", runnerToken, map[string]any{
		"display_name":      "Mac mini",
		"projects":          projects,
		"adapters":          []string{"codex"},
		"client_request_id": requestID(t),
	})
	if response.Code != http.StatusCreated {
		t.Fatalf("register runner status = %d, body = %s", response.Code, response.Body.String())
	}
	var runner state.Runner
	decodeResponse(t, response, &runner)
	return runner
}

func mustCreateReminderAPI(t *testing.T, handler http.Handler, token string, extra map[string]any) state.Reminder {
	t.Helper()

	body := map[string]any{
		"title":             "Review the nightly metrics",
		"description":       "All checks must pass",
		"client_request_id": requestID(t),
	}
	for key, value := range extra {
		body[key] = value
	}
	response := performJSONRequest(t, handler, http.MethodPost, "/api/v1/reminders", token, body)
	if response.Code != http.StatusCreated {
		t.Fatalf("create reminder status = %d, body = %s", response.Code, response.Body.String())
	}
	var reminder state.Reminder
	decodeResponse(t, response, &reminder)
	return reminder
}

func mustCreateManualRunAPI(t *testing.T, handler http.Handler, ownerToken string, reminderID string, policyID string) state.AgentRun {
	t.Helper()

	response := performJSONRequest(t, handler, http.MethodPost, "/api/v1/runs", ownerToken, map[string]any{
		"reminder_id":       reminderID,
		"policy_id":         policyID,
		"client_request_id": requestID(t),
	})
	if response.Code != http.StatusCreated {
		t.Fatalf("create manual run status = %d, body = %s", response.Code, response.Body.String())
	}
	var run state.AgentRun
	decodeResponse(t, response, &run)
	if run.Status != state.AgentRunStatusEligible || run.TaskContract.RunID != run.ID {
		t.Fatalf("manual run = %#v", run)
	}
	return run
}

func requestID(t *testing.T) string {
	t.Helper()
	return uuid.NewString()
}
func TestProjectAndPolicyAPIWithKindGates(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t)
	owner := bootstrapOwner(t, handler)
	device := pairActor(t, handler, owner.Token, map[string]any{
		"kind":         "device",
		"display_name": "Fabian",
		"device_name":  "iPad",
	})
	harness := pairActor(t, handler, owner.Token, map[string]any{
		"harness":      "codex",
		"display_name": "Codex",
		"device_name":  "MacBook",
	})

	project := mustCreateProjectAPI(t, handler, owner.Token, "customer-api")

	// Project writes are owner-only.
	for _, token := range []string{device.Token, harness.Token} {
		response := performJSONRequest(t, handler, http.MethodPost, "/api/v1/projects", token, map[string]any{
			"name":              "forbidden-project",
			"client_request_id": requestID(t),
		})
		if response.Code != http.StatusForbidden {
			t.Fatalf("create project status = %d, body = %s", response.Code, response.Body.String())
		}
	}

	// Reads allow owner and device; the harness is rejected.
	listResponse := performJSONRequest(t, handler, http.MethodGet, "/api/v1/projects", device.Token, nil)
	if listResponse.Code != http.StatusOK {
		t.Fatalf("device list projects status = %d, body = %s", listResponse.Code, listResponse.Body.String())
	}
	var projectList struct {
		Projects []state.Project `json:"projects"`
	}
	decodeResponse(t, listResponse, &projectList)
	if len(projectList.Projects) != 1 || projectList.Projects[0].ID != project.ID {
		t.Fatalf("projects = %#v", projectList.Projects)
	}
	harnessList := performJSONRequest(t, handler, http.MethodGet, "/api/v1/projects", harness.Token, nil)
	if harnessList.Code != http.StatusForbidden {
		t.Fatalf("harness list projects status = %d, body = %s", harnessList.Code, harnessList.Body.String())
	}
	harnessGet := performJSONRequest(t, handler, http.MethodGet, "/api/v1/projects/"+project.ID, harness.Token, nil)
	if harnessGet.Code != http.StatusForbidden {
		t.Fatalf("harness get project status = %d, body = %s", harnessGet.Code, harnessGet.Body.String())
	}

	// Owner updates with revision checking.
	updateResponse := performJSONRequest(t, handler, http.MethodPatch, "/api/v1/projects/"+project.ID, owner.Token, map[string]any{
		"description":       "Customer API v2",
		"expected_revision": 1,
		"client_request_id": requestID(t),
	})
	if updateResponse.Code != http.StatusOK {
		t.Fatalf("update project status = %d, body = %s", updateResponse.Code, updateResponse.Body.String())
	}
	var updatedProject state.Project
	decodeResponse(t, updateResponse, &updatedProject)
	if updatedProject.Description != "Customer API v2" || updatedProject.Revision != 2 {
		t.Fatalf("updated project = %#v", updatedProject)
	}
	deviceUpdate := performJSONRequest(t, handler, http.MethodPatch, "/api/v1/projects/"+project.ID, device.Token, map[string]any{
		"description":       "Nope",
		"expected_revision": 2,
		"client_request_id": requestID(t),
	})
	if deviceUpdate.Code != http.StatusForbidden {
		t.Fatalf("device update project status = %d, body = %s", deviceUpdate.Code, deviceUpdate.Body.String())
	}
	staleUpdate := performJSONRequest(t, handler, http.MethodPatch, "/api/v1/projects/"+project.ID, owner.Token, map[string]any{
		"description":       "Stale",
		"expected_revision": 1,
		"client_request_id": requestID(t),
	})
	if staleUpdate.Code != http.StatusConflict {
		t.Fatalf("stale update project status = %d, body = %s", staleUpdate.Code, staleUpdate.Body.String())
	}

	policy := mustCreatePolicyAPI(t, handler, owner.Token, project.ID)

	// The unattended allow-list is enforced with the dedicated error code.
	violationResponse := performJSONRequest(t, handler, http.MethodPost, "/api/v1/policies", owner.Token, map[string]any{
		"name":                 "deploy-nightly",
		"project_id":           project.ID,
		"adapter":              "codex",
		"mode":                 "unattended-low-risk",
		"allowed_capabilities": []string{"read_repository", "network_access"},
		"timeout_minutes":      30,
		"client_request_id":    requestID(t),
	})
	if violationResponse.Code != http.StatusForbidden {
		t.Fatalf("policy violation status = %d, body = %s", violationResponse.Code, violationResponse.Body.String())
	}
	var violation ErrorResponse
	decodeResponse(t, violationResponse, &violation)
	if violation.Code != "policy_violation" {
		t.Fatalf("policy violation code = %q", violation.Code)
	}

	policyListResponse := performJSONRequest(t, handler, http.MethodGet, "/api/v1/policies", device.Token, nil)
	if policyListResponse.Code != http.StatusOK {
		t.Fatalf("device list policies status = %d, body = %s", policyListResponse.Code, policyListResponse.Body.String())
	}
	var policyList struct {
		Policies []state.ExecutionPolicy `json:"policies"`
	}
	decodeResponse(t, policyListResponse, &policyList)
	if len(policyList.Policies) != 1 || policyList.Policies[0].ID != policy.ID {
		t.Fatalf("policies = %#v", policyList.Policies)
	}
	harnessPolicies := performJSONRequest(t, handler, http.MethodGet, "/api/v1/policies", harness.Token, nil)
	if harnessPolicies.Code != http.StatusForbidden {
		t.Fatalf("harness list policies status = %d, body = %s", harnessPolicies.Code, harnessPolicies.Body.String())
	}
	devicePolicyCreate := performJSONRequest(t, handler, http.MethodPost, "/api/v1/policies", device.Token, map[string]any{
		"name":                 "device-policy",
		"project_id":           project.ID,
		"adapter":              "codex",
		"mode":                 "supervised",
		"allowed_capabilities": []string{"read_repository"},
		"timeout_minutes":      30,
		"client_request_id":    requestID(t),
	})
	if devicePolicyCreate.Code != http.StatusForbidden {
		t.Fatalf("device create policy status = %d, body = %s", devicePolicyCreate.Code, devicePolicyCreate.Body.String())
	}

	disableResponse := performJSONRequest(t, handler, http.MethodPatch, "/api/v1/policies/"+policy.ID, owner.Token, map[string]any{
		"enabled":           false,
		"expected_revision": 1,
		"client_request_id": requestID(t),
	})
	if disableResponse.Code != http.StatusOK {
		t.Fatalf("disable policy status = %d, body = %s", disableResponse.Code, disableResponse.Body.String())
	}
	var disabled state.ExecutionPolicy
	decodeResponse(t, disableResponse, &disabled)
	if disabled.Enabled || disabled.Revision != 2 {
		t.Fatalf("disabled policy = %#v", disabled)
	}
	getResponse := performJSONRequest(t, handler, http.MethodGet, "/api/v1/policies/"+policy.ID, device.Token, nil)
	if getResponse.Code != http.StatusOK {
		t.Fatalf("device get policy status = %d, body = %s", getResponse.Code, getResponse.Body.String())
	}
}

func TestRunnerLifecycleAPIEndToEnd(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t)
	owner := bootstrapOwner(t, handler)
	project := mustCreateProjectAPI(t, handler, owner.Token, "customer-api")
	policy := mustCreatePolicyAPI(t, handler, owner.Token, project.ID)
	reminder := mustCreateReminderAPI(t, handler, owner.Token, map[string]any{
		"execution_policy_id": policy.ID,
	})

	// Only runner actors may self-register.
	ownerRegistration := performJSONRequest(t, handler, http.MethodPost, "/api/v1/runner/registration", owner.Token, map[string]any{
		"display_name":      "Not a runner",
		"projects":          []string{project.ID},
		"adapters":          []string{"codex"},
		"client_request_id": requestID(t),
	})
	if ownerRegistration.Code != http.StatusForbidden {
		t.Fatalf("owner registration status = %d, body = %s", ownerRegistration.Code, ownerRegistration.Body.String())
	}

	runnerCredential := pairActor(t, handler, owner.Token, map[string]any{
		"kind":         "runner",
		"display_name": "Mac mini",
	})
	if runnerCredential.Actor.Kind != state.ActorKindRunner {
		t.Fatalf("paired actor kind = %q", runnerCredential.Actor.Kind)
	}
	runner := mustRegisterRunnerAPI(t, handler, runnerCredential.Token, []string{project.ID})
	if runner.ID != runnerCredential.Actor.ID {
		t.Fatalf("runner ID = %q, actor ID = %q", runner.ID, runnerCredential.Actor.ID)
	}

	run := mustCreateManualRunAPI(t, handler, owner.Token, reminder.ID, policy.ID)

	// The run rides inside the reminder detail the iOS sync consumes.
	detailResponse := performJSONRequest(t, handler, http.MethodGet, "/api/v1/reminders/"+reminder.ID, owner.Token, nil)
	if detailResponse.Code != http.StatusOK {
		t.Fatalf("detail status = %d, body = %s", detailResponse.Code, detailResponse.Body.String())
	}
	var detail ReminderDetail
	decodeResponse(t, detailResponse, &detail)
	if len(detail.Runs) != 1 || detail.Runs[0].ID != run.ID {
		t.Fatalf("detail runs = %#v", detail.Runs)
	}
	if detail.Reminder.ExecutionPolicyID == nil || *detail.Reminder.ExecutionPolicyID != policy.ID {
		t.Fatalf("detail reminder policy = %#v", detail.Reminder.ExecutionPolicyID)
	}

	claimResponse := performJSONRequest(t, handler, http.MethodPost, "/api/v1/runner/claims?wait_seconds=0", runnerCredential.Token, nil)
	if claimResponse.Code != http.StatusOK {
		t.Fatalf("claim status = %d, body = %s", claimResponse.Code, claimResponse.Body.String())
	}
	var claimed state.AgentRun
	decodeResponse(t, claimResponse, &claimed)
	if claimed.Status != state.AgentRunStatusClaimed || claimed.RunnerID == nil || *claimed.RunnerID != runner.ID {
		t.Fatalf("claimed run = %#v", claimed)
	}
	if claimed.TaskContract.ContractHash == "" || claimed.TaskContract.PolicyID != policy.ID {
		t.Fatalf("task contract = %#v", claimed.TaskContract)
	}

	// Nothing left to claim.
	emptyClaim := performJSONRequest(t, handler, http.MethodPost, "/api/v1/runner/claims?wait_seconds=0", runnerCredential.Token, nil)
	if emptyClaim.Code != http.StatusConflict {
		t.Fatalf("empty claim status = %d, body = %s", emptyClaim.Code, emptyClaim.Body.String())
	}
	var notClaimable ErrorResponse
	decodeResponse(t, emptyClaim, &notClaimable)
	if notClaimable.Code != "not_claimable" {
		t.Fatalf("empty claim code = %q", notClaimable.Code)
	}

	// Owner and harness cannot drive the runner lifecycle.
	for _, token := range []string{owner.Token} {
		response := performJSONRequest(t, handler, http.MethodPost, "/api/v1/runner/claims?wait_seconds=0", token, nil)
		if response.Code != http.StatusForbidden {
			t.Fatalf("owner claim status = %d, body = %s", response.Code, response.Body.String())
		}
	}

	startedResponse := performJSONRequest(t, handler, http.MethodPost, "/api/v1/runner/runs/"+run.ID+"/events", runnerCredential.Token, map[string]any{
		"event":             "started",
		"detail":            "adapter launched",
		"expected_revision": claimed.Revision,
	})
	if startedResponse.Code != http.StatusOK {
		t.Fatalf("started status = %d, body = %s", startedResponse.Code, startedResponse.Body.String())
	}
	var running state.AgentRun
	decodeResponse(t, startedResponse, &running)
	if running.Status != state.AgentRunStatusRunning || running.StartedAt == nil {
		t.Fatalf("running run = %#v", running)
	}

	completeResponse := performJSONRequest(t, handler, http.MethodPost, "/api/v1/runner/runs/"+run.ID+"/complete", runnerCredential.Token, map[string]any{
		"outcome":           "succeeded",
		"result_summary":    "review landed",
		"exit_code":         0,
		"expected_revision": running.Revision,
		"client_request_id": requestID(t),
	})
	if completeResponse.Code != http.StatusOK {
		t.Fatalf("complete status = %d, body = %s", completeResponse.Code, completeResponse.Body.String())
	}
	var completed state.AgentRun
	decodeResponse(t, completeResponse, &completed)
	if completed.Status != state.AgentRunStatusSucceeded || completed.FinishedAt == nil || completed.ResultSummary != "review landed" {
		t.Fatalf("completed run = %#v", completed)
	}

	// Owner reads the run surface; the harness is rejected; filters apply.
	runsResponse := performJSONRequest(t, handler, http.MethodGet, "/api/v1/runs?reminder_id="+reminder.ID+"&status=succeeded", owner.Token, nil)
	if runsResponse.Code != http.StatusOK {
		t.Fatalf("runs status = %d, body = %s", runsResponse.Code, runsResponse.Body.String())
	}
	var runsList struct {
		Runs []state.AgentRun `json:"runs"`
	}
	decodeResponse(t, runsResponse, &runsList)
	if len(runsList.Runs) != 1 || runsList.Runs[0].ID != run.ID {
		t.Fatalf("runs = %#v", runsList.Runs)
	}
	emptyFilter := performJSONRequest(t, handler, http.MethodGet, "/api/v1/runs?status=failed", owner.Token, nil)
	decodeResponse(t, emptyFilter, &runsList)
	if len(runsList.Runs) != 0 {
		t.Fatalf("failed runs = %#v", runsList.Runs)
	}
	invalidStatus := performJSONRequest(t, handler, http.MethodGet, "/api/v1/runs?status=bogus", owner.Token, nil)
	if invalidStatus.Code != http.StatusBadRequest {
		t.Fatalf("invalid status filter = %d, body = %s", invalidStatus.Code, invalidStatus.Body.String())
	}
	getRunResponse := performJSONRequest(t, handler, http.MethodGet, "/api/v1/runs/"+run.ID, owner.Token, nil)
	if getRunResponse.Code != http.StatusOK {
		t.Fatalf("get run status = %d, body = %s", getRunResponse.Code, getRunResponse.Body.String())
	}

	harness := pairActor(t, handler, owner.Token, map[string]any{
		"harness":      "codex",
		"display_name": "Codex",
		"device_name":  "MacBook",
	})
	harnessRuns := performJSONRequest(t, handler, http.MethodGet, "/api/v1/runs", harness.Token, nil)
	if harnessRuns.Code != http.StatusForbidden {
		t.Fatalf("harness list runs status = %d, body = %s", harnessRuns.Code, harnessRuns.Body.String())
	}
	harnessRunners := performJSONRequest(t, handler, http.MethodGet, "/api/v1/runners", harness.Token, nil)
	if harnessRunners.Code != http.StatusForbidden {
		t.Fatalf("harness list runners status = %d, body = %s", harnessRunners.Code, harnessRunners.Body.String())
	}

	runnersResponse := performJSONRequest(t, handler, http.MethodGet, "/api/v1/runners", owner.Token, nil)
	if runnersResponse.Code != http.StatusOK {
		t.Fatalf("runners status = %d, body = %s", runnersResponse.Code, runnersResponse.Body.String())
	}
	var runnersList struct {
		Runners []state.Runner `json:"runners"`
	}
	decodeResponse(t, runnersResponse, &runnersList)
	if len(runnersList.Runners) != 1 || runnersList.Runners[0].ID != runner.ID {
		t.Fatalf("runners = %#v", runnersList.Runners)
	}

	updateRunnerResponse := performJSONRequest(t, handler, http.MethodPatch, "/api/v1/runners/"+runner.ID, owner.Token, map[string]any{
		"display_name":      "Studio runner",
		"expected_revision": runnersList.Runners[0].Revision,
		"client_request_id": requestID(t),
	})
	if updateRunnerResponse.Code != http.StatusOK {
		t.Fatalf("update runner status = %d, body = %s", updateRunnerResponse.Code, updateRunnerResponse.Body.String())
	}
	var updatedRunner state.Runner
	decodeResponse(t, updateRunnerResponse, &updatedRunner)
	if updatedRunner.DisplayName != "Studio runner" {
		t.Fatalf("updated runner = %#v", updatedRunner)
	}

	device := pairActor(t, handler, owner.Token, map[string]any{
		"kind":         "device",
		"display_name": "Fabian",
		"device_name":  "iPad",
	})
	deviceRunners := performJSONRequest(t, handler, http.MethodGet, "/api/v1/runners", device.Token, nil)
	if deviceRunners.Code != http.StatusOK {
		t.Fatalf("device list runners status = %d, body = %s", deviceRunners.Code, deviceRunners.Body.String())
	}
	deviceUpdate := performJSONRequest(t, handler, http.MethodPatch, "/api/v1/runners/"+runner.ID, device.Token, map[string]any{
		"display_name":      "Nope",
		"expected_revision": updatedRunner.Revision,
		"client_request_id": requestID(t),
	})
	if deviceUpdate.Code != http.StatusForbidden {
		t.Fatalf("device update runner status = %d, body = %s", deviceUpdate.Code, deviceUpdate.Body.String())
	}
	deviceManualRun := performJSONRequest(t, handler, http.MethodPost, "/api/v1/runs", device.Token, map[string]any{
		"reminder_id":       reminder.ID,
		"policy_id":         policy.ID,
		"client_request_id": requestID(t),
	})
	if deviceManualRun.Code != http.StatusForbidden {
		t.Fatalf("device manual run status = %d, body = %s", deviceManualRun.Code, deviceManualRun.Body.String())
	}
}

func TestRunApprovalAndCancelAPI(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t)
	owner := bootstrapOwner(t, handler)
	project := mustCreateProjectAPI(t, handler, owner.Token, "customer-api")
	policy := mustCreatePolicyAPI(t, handler, owner.Token, project.ID)
	reminder := mustCreateReminderAPI(t, handler, owner.Token, nil)
	runnerCredential := pairActor(t, handler, owner.Token, map[string]any{
		"kind":         "runner",
		"display_name": "Mac mini",
	})
	mustRegisterRunnerAPI(t, handler, runnerCredential.Token, []string{project.ID})

	run := mustCreateManualRunAPI(t, handler, owner.Token, reminder.ID, policy.ID)
	claimResponse := performJSONRequest(t, handler, http.MethodPost, "/api/v1/runner/claims", runnerCredential.Token, nil)
	var claimed state.AgentRun
	decodeResponse(t, claimResponse, &claimed)
	startedResponse := performJSONRequest(t, handler, http.MethodPost, "/api/v1/runner/runs/"+run.ID+"/events", runnerCredential.Token, map[string]any{
		"event":             "started",
		"expected_revision": claimed.Revision,
	})
	var running state.AgentRun
	decodeResponse(t, startedResponse, &running)

	// Approving a run that is not waiting conflicts.
	earlyApprove := performJSONRequest(t, handler, http.MethodPost, "/api/v1/runs/"+run.ID+"/approval", owner.Token, map[string]any{
		"approved":          true,
		"expected_revision": running.Revision,
		"client_request_id": requestID(t),
	})
	if earlyApprove.Code != http.StatusConflict {
		t.Fatalf("early approve status = %d, body = %s", earlyApprove.Code, earlyApprove.Body.String())
	}
	var conflict ErrorResponse
	decodeResponse(t, earlyApprove, &conflict)
	if conflict.Code != "run_state_conflict" {
		t.Fatalf("early approve code = %q", conflict.Code)
	}

	approvalRequest := performJSONRequest(t, handler, http.MethodPost, "/api/v1/runner/runs/"+run.ID+"/approval", runnerCredential.Token, map[string]any{
		"capability":        "deploy",
		"reason":            "ship the reviewed change",
		"expected_revision": running.Revision,
	})
	if approvalRequest.Code != http.StatusOK {
		t.Fatalf("approval request status = %d, body = %s", approvalRequest.Code, approvalRequest.Body.String())
	}
	var waiting state.AgentRun
	decodeResponse(t, approvalRequest, &waiting)
	if waiting.Status != state.AgentRunStatusNeedsApproval || waiting.ApprovalCapability != "deploy" {
		t.Fatalf("waiting run = %#v", waiting)
	}

	// Devices watch but never decide.
	device := pairActor(t, handler, owner.Token, map[string]any{
		"kind":         "device",
		"display_name": "Fabian",
		"device_name":  "iPad",
	})
	deviceApprove := performJSONRequest(t, handler, http.MethodPost, "/api/v1/runs/"+run.ID+"/approval", device.Token, map[string]any{
		"approved":          true,
		"expected_revision": waiting.Revision,
		"client_request_id": requestID(t),
	})
	if deviceApprove.Code != http.StatusForbidden {
		t.Fatalf("device approve status = %d, body = %s", deviceApprove.Code, deviceApprove.Body.String())
	}
	deviceGet := performJSONRequest(t, handler, http.MethodGet, "/api/v1/runs/"+run.ID, device.Token, nil)
	if deviceGet.Code != http.StatusOK {
		t.Fatalf("device get run status = %d, body = %s", deviceGet.Code, deviceGet.Body.String())
	}

	approveResponse := performJSONRequest(t, handler, http.MethodPost, "/api/v1/runs/"+run.ID+"/approval", owner.Token, map[string]any{
		"approved":          true,
		"expected_revision": waiting.Revision,
		"client_request_id": requestID(t),
	})
	if approveResponse.Code != http.StatusOK {
		t.Fatalf("approve status = %d, body = %s", approveResponse.Code, approveResponse.Body.String())
	}
	var resumed state.AgentRun
	decodeResponse(t, approveResponse, &resumed)
	if resumed.Status != state.AgentRunStatusClaimed || resumed.LeaseExpiresAt == nil || resumed.ApprovalCapability != "" {
		t.Fatalf("resumed run = %#v", resumed)
	}

	// Declining a second run cancels it with the declined failure code.
	second := mustCreateManualRunAPI(t, handler, owner.Token, reminder.ID, policy.ID)
	secondClaim := performJSONRequest(t, handler, http.MethodPost, "/api/v1/runner/claims", runnerCredential.Token, nil)
	var secondClaimed state.AgentRun
	decodeResponse(t, secondClaim, &secondClaimed)
	if secondClaimed.ID != second.ID {
		t.Fatalf("second claim = %#v, want %s", secondClaimed, second.ID)
	}
	secondStarted := performJSONRequest(t, handler, http.MethodPost, "/api/v1/runner/runs/"+second.ID+"/events", runnerCredential.Token, map[string]any{
		"event":             "started",
		"expected_revision": secondClaimed.Revision,
	})
	var secondRunning state.AgentRun
	decodeResponse(t, secondStarted, &secondRunning)
	secondApproval := performJSONRequest(t, handler, http.MethodPost, "/api/v1/runner/runs/"+second.ID+"/approval", runnerCredential.Token, map[string]any{
		"capability":        "network_access",
		"expected_revision": secondRunning.Revision,
	})
	var secondWaiting state.AgentRun
	decodeResponse(t, secondApproval, &secondWaiting)
	declineResponse := performJSONRequest(t, handler, http.MethodPost, "/api/v1/runs/"+second.ID+"/approval", owner.Token, map[string]any{
		"approved":          false,
		"expected_revision": secondWaiting.Revision,
		"client_request_id": requestID(t),
	})
	if declineResponse.Code != http.StatusOK {
		t.Fatalf("decline status = %d, body = %s", declineResponse.Code, declineResponse.Body.String())
	}
	var declined state.AgentRun
	decodeResponse(t, declineResponse, &declined)
	if declined.Status != state.AgentRunStatusCancelled || declined.FailureCode != "approval_declined" {
		t.Fatalf("declined run = %#v", declined)
	}

	// Cancelling the resumed run works once; a terminal cancel conflicts.
	cancelResponse := performJSONRequest(t, handler, http.MethodPost, "/api/v1/runs/"+run.ID+"/cancel", owner.Token, map[string]any{
		"expected_revision": resumed.Revision,
		"client_request_id": requestID(t),
	})
	if cancelResponse.Code != http.StatusOK {
		t.Fatalf("cancel status = %d, body = %s", cancelResponse.Code, cancelResponse.Body.String())
	}
	var cancelled state.AgentRun
	decodeResponse(t, cancelResponse, &cancelled)
	if cancelled.Status != state.AgentRunStatusCancelled || cancelled.FinishedAt == nil {
		t.Fatalf("cancelled run = %#v", cancelled)
	}
	recancel := performJSONRequest(t, handler, http.MethodPost, "/api/v1/runs/"+run.ID+"/cancel", owner.Token, map[string]any{
		"expected_revision": cancelled.Revision,
		"client_request_id": requestID(t),
	})
	if recancel.Code != http.StatusConflict {
		t.Fatalf("re-cancel status = %d, body = %s", recancel.Code, recancel.Body.String())
	}
}

func TestReminderExecutionPolicyFieldAPI(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t)
	owner := bootstrapOwner(t, handler)
	project := mustCreateProjectAPI(t, handler, owner.Token, "customer-api")
	policy := mustCreatePolicyAPI(t, handler, owner.Token, project.ID)

	// Unknown request fields stay rejected.
	unknownField := performJSONRequest(t, handler, http.MethodPost, "/api/v1/reminders", owner.Token, map[string]any{
		"title":             "Bogus",
		"client_request_id": requestID(t),
		"bogus_field":       true,
	})
	if unknownField.Code != http.StatusBadRequest {
		t.Fatalf("unknown field status = %d, body = %s", unknownField.Code, unknownField.Body.String())
	}

	// Unknown policy IDs are invalid input.
	unknownPolicy := performJSONRequest(t, handler, http.MethodPost, "/api/v1/reminders", owner.Token, map[string]any{
		"title":               "Bogus policy",
		"client_request_id":   requestID(t),
		"execution_policy_id": "0198a5d0-0000-7000-8000-000000000000",
	})
	if unknownPolicy.Code != http.StatusBadRequest {
		t.Fatalf("unknown policy status = %d, body = %s", unknownPolicy.Code, unknownPolicy.Body.String())
	}

	reminder := mustCreateReminderAPI(t, handler, owner.Token, map[string]any{
		"execution_policy_id": policy.ID,
	})
	if reminder.ExecutionPolicyID == nil || *reminder.ExecutionPolicyID != policy.ID {
		t.Fatalf("reminder policy = %#v", reminder.ExecutionPolicyID)
	}

	// Explicit null clears the policy.
	clearResponse := performJSONRequest(t, handler, http.MethodPatch, "/api/v1/reminders/"+reminder.ID, owner.Token, map[string]any{
		"execution_policy_id": nil,
		"expected_revision":   reminder.Revision,
		"client_request_id":   requestID(t),
	})
	if clearResponse.Code != http.StatusOK {
		t.Fatalf("clear policy status = %d, body = %s", clearResponse.Code, clearResponse.Body.String())
	}
	var cleared state.Reminder
	decodeResponse(t, clearResponse, &cleared)
	if cleared.ExecutionPolicyID != nil {
		t.Fatalf("cleared policy = %#v", cleared.ExecutionPolicyID)
	}

	// Omitting the field leaves the (cleared) value untouched; setting works again.
	noopResponse := performJSONRequest(t, handler, http.MethodPatch, "/api/v1/reminders/"+reminder.ID, owner.Token, map[string]any{
		"description":       "Still passing",
		"expected_revision": cleared.Revision,
		"client_request_id": requestID(t),
	})
	var noop state.Reminder
	decodeResponse(t, noopResponse, &noop)
	if noop.ExecutionPolicyID != nil {
		t.Fatalf("noop policy = %#v", noop.ExecutionPolicyID)
	}
	setResponse := performJSONRequest(t, handler, http.MethodPatch, "/api/v1/reminders/"+reminder.ID, owner.Token, map[string]any{
		"execution_policy_id": policy.ID,
		"expected_revision":   noop.Revision,
		"client_request_id":   requestID(t),
	})
	var again state.Reminder
	decodeResponse(t, setResponse, &again)
	if again.ExecutionPolicyID == nil || *again.ExecutionPolicyID != policy.ID {
		t.Fatalf("re-attached policy = %#v", again.ExecutionPolicyID)
	}
}
