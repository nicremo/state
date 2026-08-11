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
