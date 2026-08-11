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
