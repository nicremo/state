package relay

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/nicremo/state/internal/pushcrypto"
)

func TestChallengeRegistrationAndEncryptedDelivery(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.August, 11, 20, 0, 0, 0, time.UTC)
	repository := newMemoryRepository()
	attestor := &recordingAttestor{}
	dispatcher := &recordingDispatcher{}
	handler := NewHandler(Config{
		Repository: repository,
		Attestor:   attestor,
		Dispatcher: dispatcher,
		Clock:      func() time.Time { return now },
		NewID:      func() (string, error) { return "01989fdb-0566-7975-a306-fd8f8270ab06", nil },
		NewSecret:  func() (string, error) { return "route_secret", nil },
		NewChallenge: func() (string, error) {
			return "registration_challenge", nil
		},
		Limiter: AllowAllLimiter{},
	})

	challengeResponse := performRequest(t, handler, http.MethodPost, "/v1/attest/challenges", "", map[string]any{})
	if challengeResponse.Code != http.StatusCreated {
		t.Fatalf("challenge status = %d, body = %s", challengeResponse.Code, challengeResponse.Body.String())
	}
	var challenge Challenge
	decodeRecorder(t, challengeResponse, &challenge)
	if challenge.Value != "registration_challenge" || !challenge.ExpiresAt.Equal(now.Add(5*time.Minute)) {
		t.Fatalf("challenge = %#v", challenge)
	}

	registration := map[string]any{
		"apns_token":  strings.Repeat("a", 64),
		"environment": "sandbox",
		"attestation": map[string]any{
			"key_id":    "app-attest-key",
			"object":    "attestation-object",
			"challenge": challenge.Value,
			"assertion": "registration-assertion",
		},
	}
	registerResponse := performRequest(t, handler, http.MethodPost, "/v1/routes", "", registration)
	if registerResponse.Code != http.StatusCreated {
		t.Fatalf("register status = %d, body = %s", registerResponse.Code, registerResponse.Body.String())
	}
	var registered RegistrationResponse
	decodeRecorder(t, registerResponse, &registered)
	if registered.RouteID == "" || registered.Authorization != "route_secret" {
		t.Fatalf("registration = %#v", registered)
	}
	if attestor.input.Challenge != challenge.Value || attestor.input.APNSTokenHash == "" {
		t.Fatalf("attestation input = %#v", attestor.input)
	}

	envelope := pushcrypto.Envelope{
		Version:            1,
		EphemeralPublicKey: bytes.Repeat([]byte{1}, 32),
		Nonce:              bytes.Repeat([]byte{2}, 12),
		Ciphertext:         []byte("opaque encrypted reminder"),
	}
	sendResponse := performRequest(t, handler, http.MethodPost, "/v1/routes/"+registered.RouteID+"/notifications", registered.Authorization, map[string]any{
		"kind":        "reminder",
		"collapse_id": "occurrence-01989f",
		"envelope":    envelope,
	})
	if sendResponse.Code != http.StatusAccepted {
		t.Fatalf("send status = %d, body = %s", sendResponse.Code, sendResponse.Body.String())
	}
	if len(dispatcher.notifications) != 1 {
		t.Fatalf("notification count = %d", len(dispatcher.notifications))
	}
	notification := dispatcher.notifications[0]
	if notification.APNSToken != strings.Repeat("a", 64) || notification.Environment != EnvironmentSandbox {
		t.Fatalf("notification route = %#v", notification)
	}
	if notification.CollapseID != "occurrence-01989f" || notification.PushType != PushTypeAlert {
		t.Fatalf("notification metadata = %#v", notification)
	}
	encodedPayload := string(notification.Payload)
	if strings.Contains(encodedPayload, "Prepare") {
		t.Fatalf("payload = %s", encodedPayload)
	}
	var decodedPayload struct {
		APS struct {
			InterruptionLevel string `json:"interruption-level"`
		} `json:"aps"`
		State struct {
			Envelope pushcrypto.Envelope `json:"envelope"`
		} `json:"state"`
	}
	if err := json.Unmarshal(notification.Payload, &decodedPayload); err != nil {
		t.Fatalf("decode notification payload: %v", err)
	}
	if !bytes.Equal(decodedPayload.State.Envelope.Ciphertext, envelope.Ciphertext) {
		t.Fatalf("notification envelope = %#v", decodedPayload.State.Envelope)
	}
	if decodedPayload.APS.InterruptionLevel != "time-sensitive" {
		t.Fatalf("reminder push must break through Focus, interruption-level = %q", decodedPayload.APS.InterruptionLevel)
	}
	if !strings.Contains(encodedPayload, "Neue Erinnerung") || !strings.Contains(encodedPayload, "New reminder") {
		t.Fatalf("payload lacks generic localized fallback: %s", encodedPayload)
	}
}

func TestRelayRejectsInvalidCapabilityAndRateLimit(t *testing.T) {
	t.Parallel()

	repository := newMemoryRepository()
	repository.routes["route"] = Route{
		ID:             "route",
		APNSToken:      strings.Repeat("b", 64),
		Environment:    EnvironmentProduction,
		CapabilityHash: hashCapability("correct"),
	}
	handler := NewHandler(Config{
		Repository: repository,
		Attestor:   &recordingAttestor{},
		Dispatcher: &recordingDispatcher{},
		Limiter:    &fixedLimiter{allowed: false},
	})
	payload := map[string]any{
		"kind": "sync",
		"envelope": pushcrypto.Envelope{
			Version:            1,
			EphemeralPublicKey: bytes.Repeat([]byte{1}, 32),
			Nonce:              bytes.Repeat([]byte{2}, 12),
			Ciphertext:         []byte("opaque"),
		},
	}
	unauthorized := performRequest(t, handler, http.MethodPost, "/v1/routes/route/notifications", "wrong", payload)
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status = %d", unauthorized.Code)
	}
	limited := performRequest(t, handler, http.MethodPost, "/v1/routes/route/notifications", "correct", payload)
	if limited.Code != http.StatusTooManyRequests {
		t.Fatalf("limited status = %d", limited.Code)
	}
}

func TestRunFinishedNotificationIsTimeSensitiveAlert(t *testing.T) {
	t.Parallel()

	repository := newMemoryRepository()
	repository.routes["route"] = Route{
		ID:             "route",
		APNSToken:      strings.Repeat("e", 64),
		Environment:    EnvironmentProduction,
		CapabilityHash: hashCapability("correct"),
	}
	dispatcher := &recordingDispatcher{}
	handler := NewHandler(Config{
		Repository: repository,
		Attestor:   &recordingAttestor{},
		Dispatcher: dispatcher,
		Limiter:    AllowAllLimiter{},
	})
	envelope := pushcrypto.Envelope{
		Version:            1,
		EphemeralPublicKey: bytes.Repeat([]byte{3}, 32),
		Nonce:              bytes.Repeat([]byte{4}, 12),
		Ciphertext:         []byte("sealed run outcome"),
	}
	response := performRequest(t, handler, http.MethodPost, "/v1/routes/route/notifications", "correct", map[string]any{
		"kind":        "run_finished",
		"collapse_id": "run-01989f",
		"envelope":    envelope,
	})
	if response.Code != http.StatusAccepted {
		t.Fatalf("send status = %d, body = %s", response.Code, response.Body.String())
	}
	if len(dispatcher.notifications) != 1 {
		t.Fatalf("notification count = %d", len(dispatcher.notifications))
	}
	notification := dispatcher.notifications[0]
	if notification.PushType != PushTypeAlert || notification.CollapseID != "run-01989f" {
		t.Fatalf("notification metadata = %#v", notification)
	}
	encodedPayload := string(notification.Payload)
	if strings.Contains(encodedPayload, "sealed run outcome") {
		t.Fatalf("payload leaks envelope plaintext: %s", encodedPayload)
	}
	if strings.Contains(encodedPayload, "fehlgeschlagen") || strings.Contains(encodedPayload, "failed") {
		t.Fatalf("run_finished text must stay status-neutral: %s", encodedPayload)
	}
	var decoded struct {
		APS struct {
			MutableContent    int               `json:"mutable-content"`
			Sound             string            `json:"sound"`
			Category          string            `json:"category"`
			InterruptionLevel string            `json:"interruption-level"`
			RelevanceScore    int               `json:"relevance-score"`
			Alert             map[string]string `json:"alert"`
		} `json:"aps"`
		State struct {
			Envelope pushcrypto.Envelope `json:"envelope"`
			Fallback map[string]string   `json:"fallback"`
		} `json:"state"`
	}
	if err := json.Unmarshal(notification.Payload, &decoded); err != nil {
		t.Fatalf("decode notification payload: %v", err)
	}
	if decoded.APS.Category != "STATE_RUN" || decoded.APS.InterruptionLevel != "time-sensitive" ||
		decoded.APS.RelevanceScore != 1 || decoded.APS.MutableContent != 1 || decoded.APS.Sound != "default" {
		t.Fatalf("run_finished aps = %#v", decoded.APS)
	}
	if decoded.APS.Alert["title"] != "State" || decoded.APS.Alert["body"] != "Agent run finished" {
		t.Fatalf("run_finished alert = %#v", decoded.APS.Alert)
	}
	if decoded.State.Fallback["de"] != "Agent-Lauf abgeschlossen" || decoded.State.Fallback["en"] != "Agent run finished" {
		t.Fatalf("run_finished fallback = %#v", decoded.State.Fallback)
	}
	if !bytes.Equal(decoded.State.Envelope.Ciphertext, envelope.Ciphertext) {
		t.Fatalf("notification envelope = %#v", decoded.State.Envelope)
	}
}

func TestValidNotificationKinds(t *testing.T) {
	t.Parallel()

	envelope := pushcrypto.Envelope{
		Version:            1,
		EphemeralPublicKey: bytes.Repeat([]byte{1}, 32),
		Nonce:              bytes.Repeat([]byte{2}, 12),
		Ciphertext:         []byte("opaque"),
	}
	for _, kind := range []string{"sync", "reminder", "run_finished"} {
		if !validNotification(kind, "collapse", envelope) {
			t.Fatalf("validNotification(%s) = false, want true", kind)
		}
	}
	if validNotification("heartbeat", "collapse", envelope) {
		t.Fatalf("validNotification(heartbeat) = true, want false")
	}
	if validNotification("run_finished", strings.Repeat("x", 65), envelope) {
		t.Fatalf("collapse ID longer than 64 must be rejected")
	}
	badVersion := envelope
	badVersion.Version = 2
	if validNotification("run_finished", "collapse", badVersion) {
		t.Fatalf("envelope version 2 must be rejected")
	}
	emptyCiphertext := envelope
	emptyCiphertext.Ciphertext = nil
	if validNotification("run_finished", "collapse", emptyCiphertext) {
		t.Fatalf("empty ciphertext must be rejected")
	}
	oversized := envelope
	oversized.Ciphertext = make([]byte, 3001)
	if validNotification("run_finished", "collapse", oversized) {
		t.Fatalf("ciphertext over 3000 bytes must be rejected")
	}
}

func TestRegistrationChallengeIsSingleUse(t *testing.T) {
	t.Parallel()

	repository := newMemoryRepository()
	repository.challenges["used"] = time.Now().Add(time.Minute)
	handler := NewHandler(Config{
		Repository: repository,
		Attestor:   &recordingAttestor{},
		Dispatcher: &recordingDispatcher{},
		Limiter:    AllowAllLimiter{},
	})
	request := map[string]any{
		"apns_token":  strings.Repeat("c", 64),
		"environment": "production",
		"attestation": map[string]any{"key_id": "key", "object": "object", "challenge": "used", "assertion": "assertion"},
	}
	first := performRequest(t, handler, http.MethodPost, "/v1/routes", "", request)
	if first.Code != http.StatusCreated {
		t.Fatalf("first registration status = %d, body = %s", first.Code, first.Body.String())
	}
	second := performRequest(t, handler, http.MethodPost, "/v1/routes", "", request)
	if second.Code != http.StatusUnauthorized {
		t.Fatalf("second registration status = %d", second.Code)
	}
}

type memoryRepository struct {
	challenges map[string]time.Time
	routes     map[string]Route
}

func newMemoryRepository() *memoryRepository {
	return &memoryRepository{challenges: make(map[string]time.Time), routes: make(map[string]Route)}
}

func (repository *memoryRepository) CreateChallenge(_ context.Context, challenge Challenge) error {
	repository.challenges[challenge.Value] = challenge.ExpiresAt
	return nil
}

func (repository *memoryRepository) ConsumeChallenge(_ context.Context, value string, now time.Time) error {
	expiresAt, ok := repository.challenges[value]
	if !ok || now.After(expiresAt) {
		return ErrInvalidAttestation
	}
	delete(repository.challenges, value)
	return nil
}

func (repository *memoryRepository) CreateRoute(_ context.Context, route Route) error {
	repository.routes[route.ID] = route
	return nil
}

func (repository *memoryRepository) GetRoute(_ context.Context, id string) (Route, error) {
	route, ok := repository.routes[id]
	if !ok {
		return Route{}, ErrRouteNotFound
	}
	return route, nil
}

func (repository *memoryRepository) UpdateRouteToken(_ context.Context, id string, token string) error {
	route, ok := repository.routes[id]
	if !ok {
		return ErrRouteNotFound
	}
	route.APNSToken = token
	repository.routes[id] = route
	return nil
}

func (repository *memoryRepository) DeleteRoute(_ context.Context, id string) error {
	delete(repository.routes, id)
	return nil
}

type recordingAttestor struct {
	input AttestationInput
}

func (attestor *recordingAttestor) Verify(_ context.Context, input AttestationInput) error {
	attestor.input = input
	return nil
}

type recordingDispatcher struct {
	notifications []Notification
}

func (dispatcher *recordingDispatcher) Send(_ context.Context, notification Notification) error {
	dispatcher.notifications = append(dispatcher.notifications, notification)
	return nil
}

type fixedLimiter struct {
	allowed bool
}

func (limiter *fixedLimiter) Allow(_ string) bool {
	return limiter.allowed
}

func performRequest(t *testing.T, handler http.Handler, method string, path string, token string, value any) *httptest.ResponseRecorder {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	request := httptest.NewRequest(method, path, bytes.NewReader(encoded))
	request.Header.Set("Content-Type", "application/json")
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func decodeRecorder(t *testing.T, recorder *httptest.ResponseRecorder, value any) {
	t.Helper()
	if err := json.NewDecoder(recorder.Body).Decode(value); err != nil {
		t.Fatalf("Decode() error = %v, body = %s", err, recorder.Body.String())
	}
}
