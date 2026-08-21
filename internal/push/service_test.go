package push

import (
	"bytes"
	"context"
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	stateauth "github.com/nicremo/state/internal/auth"
	"github.com/nicremo/state/internal/pushcrypto"
	"github.com/nicremo/state/internal/relay"
	"github.com/nicremo/state/internal/state"
	"github.com/nicremo/state/internal/store"
	"github.com/pocketbase/pocketbase"
)

func TestDeviceRouteRegistrationAndOccurrenceConfirmation(t *testing.T) {
	t.Parallel()

	app, stateService, owner := newPushTestApplication(t)
	repository, err := NewRepository(app, bytes.Repeat([]byte{0x31}, 32))
	if err != nil {
		t.Fatalf("NewRepository() error = %v", err)
	}
	privateKey, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}
	route, err := repository.RegisterDevice(context.Background(), owner.Actor, RegisterDeviceInput{
		RelayURL:      "https://relay.example.com",
		RouteID:       "0198a08d-1ca1-7122-bf7d-c6f428ad7398",
		Authorization: "relay_capability_secret",
		PublicKey:     privateKey.PublicKey().Bytes(),
	})
	if err != nil {
		t.Fatalf("RegisterDevice() error = %v", err)
	}
	if route.ActorID != owner.Actor.ID || route.Authorization != "" {
		t.Fatalf("public route = %#v", route)
	}
	storedRoutes, err := repository.ListRoutes(context.Background(), "")
	if err != nil {
		t.Fatalf("ListRoutes() error = %v", err)
	}
	if len(storedRoutes) != 1 || storedRoutes[0].Authorization != "relay_capability_secret" {
		t.Fatalf("stored routes = %#v", storedRoutes)
	}

	reminder, err := stateService.CreateReminder(context.Background(), owner.Actor, state.CreateReminderInput{
		Title:           "Offline protected reminder",
		ClientRequestID: "0198a08d-1ca1-7b7a-8bd1-99b019e6cda1",
		Schedule: &state.Schedule{
			LocalDate: "2026-08-17",
			LocalTime: "09:00",
			TimeZone:  "Europe/Copenhagen",
			Mode:      state.TimeZoneModeFloating,
		},
	})
	if err != nil {
		t.Fatalf("CreateReminder() error = %v", err)
	}
	occurrences, err := stateService.ListOccurrences(context.Background(), reminder.ID, state.OccurrenceListOptions{Limit: 10})
	if err != nil || len(occurrences) != 1 {
		t.Fatalf("ListOccurrences() = %#v, %v", occurrences, err)
	}
	before, err := repository.ListUnconfirmedRoutes(context.Background(), occurrences[0].ID)
	if err != nil || len(before) != 1 {
		t.Fatalf("unconfirmed routes before = %#v, %v", before, err)
	}
	if err := repository.ConfirmOccurrences(context.Background(), owner.Actor, []string{occurrences[0].ID}); err != nil {
		t.Fatalf("ConfirmOccurrences() error = %v", err)
	}
	after, err := repository.ListUnconfirmedRoutes(context.Background(), occurrences[0].ID)
	if err != nil || len(after) != 0 {
		t.Fatalf("unconfirmed routes after = %#v, %v", after, err)
	}
}

func TestHTTPSenderEncryptsPayloadForDevice(t *testing.T) {
	t.Parallel()

	privateKey, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}
	var received struct {
		Kind       string              `json:"kind"`
		CollapseID string              `json:"collapse_id"`
		Envelope   pushcrypto.Envelope `json:"envelope"`
	}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v1/routes/route-id/notifications" || request.Header.Get("Authorization") != "Bearer route-secret" {
			http.Error(writer, "invalid route", http.StatusUnauthorized)
			return
		}
		if err := json.NewDecoder(request.Body).Decode(&received); err != nil {
			http.Error(writer, "invalid JSON", http.StatusBadRequest)
			return
		}
		writer.WriteHeader(http.StatusAccepted)
	}))
	t.Cleanup(server.Close)
	sender := NewHTTPSender(server.Client())
	plaintext := []byte(`{"title":"Secret reminder","occurrence_id":"occurrence"}`)
	route := DeviceRoute{
		RelayURL:      server.URL,
		RouteID:       "route-id",
		Authorization: "route-secret",
		PublicKey:     privateKey.PublicKey().Bytes(),
	}
	if err := sender.Send(context.Background(), route, "reminder", "occurrence", plaintext); err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if received.Kind != "reminder" || received.CollapseID != "occurrence" {
		t.Fatalf("received metadata = %#v", received)
	}
	opened, err := pushcrypto.Open(privateKey.Bytes(), received.Envelope, []byte(route.RouteID))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if !bytes.Equal(opened, plaintext) || strings.Contains(string(received.Envelope.Ciphertext), "Secret reminder") {
		t.Fatalf("opened payload = %s", opened)
	}
}

func TestServiceDeliversOnlyUnconfirmedOccurrenceOnce(t *testing.T) {
	t.Parallel()

	app, stateService, owner := newPushTestApplication(t)
	repository, err := NewRepository(app, bytes.Repeat([]byte{0x52}, 32))
	if err != nil {
		t.Fatalf("NewRepository() error = %v", err)
	}
	privateKey, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}
	if _, err := repository.RegisterDevice(context.Background(), owner.Actor, RegisterDeviceInput{
		RelayURL:      "https://relay.example.com",
		RouteID:       "0198a08d-1ca1-7122-bf7d-c6f428ad7398",
		Authorization: "route-secret",
		PublicKey:     privateKey.PublicKey().Bytes(),
	}); err != nil {
		t.Fatalf("RegisterDevice() error = %v", err)
	}
	reminder, err := stateService.CreateReminder(context.Background(), owner.Actor, state.CreateReminderInput{
		Title:           "Fallback target",
		Description:     "Visible only when local scheduling is not confirmed.",
		ClientRequestID: "0198a08d-1ca1-7fb4-8dd8-22892e392f8d",
		Schedule: &state.Schedule{
			LocalDate: "2026-08-17",
			LocalTime: "09:00",
			TimeZone:  "Europe/Copenhagen",
			Mode:      state.TimeZoneModeFloating,
		},
	})
	if err != nil {
		t.Fatalf("CreateReminder() error = %v", err)
	}
	recorder := &recordingSender{}
	service := NewService(repository, recorder)
	location, err := time.LoadLocation("Europe/Copenhagen")
	if err != nil {
		t.Fatalf("LoadLocation() error = %v", err)
	}
	start := time.Date(2026, time.August, 17, 8, 59, 0, 0, location)
	delivered, err := service.DeliverDue(context.Background(), start.UTC(), start.Add(2*time.Minute).UTC())
	if err != nil {
		t.Fatalf("DeliverDue() error = %v", err)
	}
	if delivered != 1 || len(recorder.deliveries) != 1 {
		t.Fatalf("delivered = %d, recordings = %#v", delivered, recorder.deliveries)
	}
	if recorder.deliveries[0].kind != "reminder" || recorder.deliveries[0].collapseID == "" || !strings.Contains(string(recorder.deliveries[0].plaintext), reminder.Title) {
		t.Fatalf("delivery = %#v", recorder.deliveries[0])
	}
	delivered, err = service.DeliverDue(context.Background(), start.UTC(), start.Add(2*time.Minute).UTC())
	if err != nil || delivered != 0 || len(recorder.deliveries) != 1 {
		t.Fatalf("second delivery = %d, recordings = %d, error = %v", delivered, len(recorder.deliveries), err)
	}
}

func TestNotifyRunFinishedDeliversToAllDeviceRoutes(t *testing.T) {
	t.Parallel()

	app, _, owner := newPushTestApplication(t)
	repository, err := NewRepository(app, bytes.Repeat([]byte{0x63}, 32))
	if err != nil {
		t.Fatalf("NewRepository() error = %v", err)
	}
	authManager, err := stateauth.NewManager(app, "bootstrap-secret")
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	pairing, err := authManager.CreatePairingCode(context.Background(), owner.Actor, stateauth.PairingCodeRequest{
		Kind:        state.ActorKindDevice,
		DisplayName: "Fabian",
		DeviceName:  "iPad",
	})
	if err != nil {
		t.Fatalf("CreatePairingCode() error = %v", err)
	}
	device, err := authManager.ExchangePairingCode(context.Background(), pairing.Code)
	if err != nil {
		t.Fatalf("ExchangePairingCode() error = %v", err)
	}
	ownerKey := newRouteKey(t)
	deviceKey := newRouteKey(t)
	registerRoute(t, repository, owner.Actor, "0198a08d-1ca1-7122-bf7d-c6f428ad7398", "owner-route-secret", ownerKey)
	registerRoute(t, repository, device.Actor, "0198a08d-1ca1-7a31-9f62-0c85e07e8a10", "device-route-secret", deviceKey)

	recorder := &recordingSender{}
	service := NewService(repository, recorder)
	finishedAt := time.Date(2026, time.August, 17, 10, 0, 0, 0, time.UTC)
	occurrenceID := "0198a08d-1ca1-70f2-88d3-0a49e1e2c901"
	run := state.AgentRun{
		ID:            "0198a08d-1ca1-7c44-8a5b-4e2f6a91b201",
		ReminderID:    "0198a08d-1ca1-75b1-9c42-7d0e5f31a8b2",
		OccurrenceID:  &occurrenceID,
		Status:        state.AgentRunStatusSucceeded,
		FinishedAt:    &finishedAt,
		ResultSummary: "must not leak into the payload",
	}
	if err := service.NotifyRunFinished(context.Background(), run, "Weekly report"); err != nil {
		t.Fatalf("NotifyRunFinished() error = %v", err)
	}
	if len(recorder.deliveries) != 2 {
		t.Fatalf("deliveries = %#v", recorder.deliveries)
	}
	secrets := map[string]string{
		owner.Actor.ID:  "owner-route-secret",
		device.Actor.ID: "device-route-secret",
	}
	for _, delivery := range recorder.deliveries {
		if delivery.kind != "run_finished" || delivery.collapseID != run.ID {
			t.Fatalf("delivery metadata = %#v", delivery)
		}
		if delivery.route.Authorization != secrets[delivery.route.ActorID] {
			t.Fatalf("route authorization for %s was not decrypted: %#v", delivery.route.ActorID, delivery.route)
		}
		payload := map[string]any{}
		if err := json.Unmarshal(delivery.plaintext, &payload); err != nil {
			t.Fatalf("payload is not JSON: %v", err)
		}
		if len(payload) != 7 {
			t.Fatalf("payload keys = %#v", payload)
		}
		if payload["kind"] != "run_finished" || payload["run_id"] != run.ID || payload["reminder_id"] != run.ReminderID ||
			payload["occurrence_id"] != occurrenceID || payload["status"] != "succeeded" || payload["title"] != "Weekly report" ||
			payload["finished_at"] != finishedAt.Format(time.RFC3339Nano) {
			t.Fatalf("payload = %#v", payload)
		}
		if strings.Contains(string(delivery.plaintext), "must not leak") {
			t.Fatalf("payload carries result summary: %s", delivery.plaintext)
		}
	}

	recorder.deliveries = nil
	runWithoutOccurrence := state.AgentRun{
		ID:         "0198a08d-1ca1-7aa1-b1f1-9e8d7c6b5a4f",
		ReminderID: "0198a08d-1ca1-75b1-9c42-7d0e5f31a8b2",
		Status:     state.AgentRunStatusFailed,
		FinishedAt: &finishedAt,
	}
	if err := service.NotifyRunFinished(context.Background(), runWithoutOccurrence, "Weekly report"); err != nil {
		t.Fatalf("NotifyRunFinished() error = %v", err)
	}
	if len(recorder.deliveries) != 2 {
		t.Fatalf("deliveries = %#v", recorder.deliveries)
	}
	payload := map[string]any{}
	if err := json.Unmarshal(recorder.deliveries[0].plaintext, &payload); err != nil {
		t.Fatalf("payload is not JSON: %v", err)
	}
	if _, present := payload["occurrence_id"]; present || len(payload) != 6 {
		t.Fatalf("payload keys = %#v", payload)
	}
}

func TestNotifyRunFinishedStatusFilter(t *testing.T) {
	t.Parallel()

	app, _, owner := newPushTestApplication(t)
	repository, err := NewRepository(app, bytes.Repeat([]byte{0x74}, 32))
	if err != nil {
		t.Fatalf("NewRepository() error = %v", err)
	}
	registerRoute(t, repository, owner.Actor, "0198a08d-1ca1-7122-bf7d-c6f428ad7398", "route-secret", newRouteKey(t))
	recorder := &recordingSender{}
	service := NewService(repository, recorder)
	run := state.AgentRun{
		ID:         "0198a08d-1ca1-7c44-8a5b-4e2f6a91b201",
		ReminderID: "0198a08d-1ca1-75b1-9c42-7d0e5f31a8b2",
	}
	for _, status := range []state.AgentRunStatus{
		state.AgentRunStatusPlanned,
		state.AgentRunStatusEligible,
		state.AgentRunStatusClaimed,
		state.AgentRunStatusRunning,
		state.AgentRunStatusCancelled,
		state.AgentRunStatusExpired,
	} {
		run.Status = status
		if err := service.NotifyRunFinished(context.Background(), run, "Weekly report"); err != nil {
			t.Fatalf("NotifyRunFinished(%s) error = %v", status, err)
		}
		if len(recorder.deliveries) != 0 {
			t.Fatalf("NotifyRunFinished(%s) delivered %#v", status, recorder.deliveries)
		}
	}
	for _, status := range []state.AgentRunStatus{state.AgentRunStatusSucceeded, state.AgentRunStatusFailed, state.AgentRunStatusNeedsApproval} {
		recorder.deliveries = nil
		run.Status = status
		run.FinishedAt = nil
		if err := service.NotifyRunFinished(context.Background(), run, "Weekly report"); err != nil {
			t.Fatalf("NotifyRunFinished(%s) error = %v", status, err)
		}
		if len(recorder.deliveries) != 1 {
			t.Fatalf("NotifyRunFinished(%s) deliveries = %#v", status, recorder.deliveries)
		}
		payload := map[string]any{}
		if err := json.Unmarshal(recorder.deliveries[0].plaintext, &payload); err != nil {
			t.Fatalf("payload is not JSON: %v", err)
		}
		if payload["status"] != string(status) {
			t.Fatalf("payload status = %#v, want %s", payload["status"], status)
		}
		finishedAt, ok := payload["finished_at"].(string)
		if !ok || finishedAt == "" {
			t.Fatalf("payload finished_at = %#v", payload["finished_at"])
		}
		if _, err := time.Parse(time.RFC3339Nano, finishedAt); err != nil {
			t.Fatalf("finished_at does not parse: %v", err)
		}
	}
}

func TestNotifyRunFinishedIsNilSafe(t *testing.T) {
	t.Parallel()

	run := state.AgentRun{ID: "run", ReminderID: "reminder", Status: state.AgentRunStatusSucceeded}
	var service *Service
	if err := service.NotifyRunFinished(context.Background(), run, "title"); err != nil {
		t.Fatalf("nil service error = %v", err)
	}
	if err := NewService(nil, &recordingSender{}).NotifyRunFinished(context.Background(), run, "title"); err != nil {
		t.Fatalf("nil repository error = %v", err)
	}
	if err := NewService(&Repository{}, nil).NotifyRunFinished(context.Background(), run, "title"); err != nil {
		t.Fatalf("nil sender error = %v", err)
	}
}

func TestNotifyRunFinishedAccumulatesRouteErrors(t *testing.T) {
	t.Parallel()

	app, _, owner := newPushTestApplication(t)
	repository, err := NewRepository(app, bytes.Repeat([]byte{0x85}, 32))
	if err != nil {
		t.Fatalf("NewRepository() error = %v", err)
	}
	authManager, err := stateauth.NewManager(app, "bootstrap-secret")
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	pairing, err := authManager.CreatePairingCode(context.Background(), owner.Actor, stateauth.PairingCodeRequest{
		Kind:        state.ActorKindDevice,
		DisplayName: "Fabian",
		DeviceName:  "iPad",
	})
	if err != nil {
		t.Fatalf("CreatePairingCode() error = %v", err)
	}
	device, err := authManager.ExchangePairingCode(context.Background(), pairing.Code)
	if err != nil {
		t.Fatalf("ExchangePairingCode() error = %v", err)
	}
	registerRoute(t, repository, owner.Actor, "0198a08d-1ca1-7122-bf7d-c6f428ad7398", "owner-route-secret", newRouteKey(t))
	registerRoute(t, repository, device.Actor, "0198a08d-1ca1-7a31-9f62-0c85e07e8a10", "device-route-secret", newRouteKey(t))
	failing := &failingSender{}
	service := NewService(repository, failing)
	run := state.AgentRun{
		ID:         "0198a08d-1ca1-7c44-8a5b-4e2f6a91b201",
		ReminderID: "0198a08d-1ca1-75b1-9c42-7d0e5f31a8b2",
		Status:     state.AgentRunStatusFailed,
	}
	err = service.NotifyRunFinished(context.Background(), run, "Weekly report")
	if err == nil || !strings.Contains(err.Error(), owner.Actor.ID) || !strings.Contains(err.Error(), device.Actor.ID) {
		t.Fatalf("NotifyRunFinished() error = %v", err)
	}
	if failing.attempts != 2 {
		t.Fatalf("attempts = %d, want one per route", failing.attempts)
	}
}

func TestHTTPSenderKindAllowList(t *testing.T) {
	t.Parallel()

	privateKey, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		requests++
		writer.WriteHeader(http.StatusAccepted)
	}))
	t.Cleanup(server.Close)
	sender := NewHTTPSender(server.Client())
	route := DeviceRoute{
		RelayURL:      server.URL,
		RouteID:       "route-id",
		Authorization: "route-secret",
		PublicKey:     privateKey.PublicKey().Bytes(),
	}
	if err := sender.Send(context.Background(), route, "run_finished", "run-01989f", []byte(`{"kind":"run_finished"}`)); err != nil {
		t.Fatalf("Send(run_finished) error = %v", err)
	}
	if err := sender.Send(context.Background(), route, "heartbeat", "run-01989f", []byte(`{"kind":"heartbeat"}`)); err == nil {
		t.Fatalf("Send(heartbeat) error = nil, want rejection")
	}
	if requests != 1 {
		t.Fatalf("requests = %d, want only the run_finished send", requests)
	}
}

func TestNotifyRunFinishedThroughRelay(t *testing.T) {
	t.Parallel()

	app, _, owner := newPushTestApplication(t)
	repository, err := NewRepository(app, bytes.Repeat([]byte{0x96}, 32))
	if err != nil {
		t.Fatalf("NewRepository() error = %v", err)
	}
	deviceKey := newRouteKey(t)
	routeID := "0198a08d-1ca1-7122-bf7d-c6f428ad7398"
	capability := "run-route-secret"
	capabilityHash := sha256.Sum256([]byte(capability))
	dispatcher := &recordingRelayDispatcher{}
	relayHandler := relay.NewHandler(relay.Config{
		Repository: &singleRouteRepository{route: relay.Route{
			ID:             routeID,
			APNSToken:      strings.Repeat("f", 64),
			Environment:    relay.EnvironmentSandbox,
			CapabilityHash: capabilityHash[:],
		}},
		Attestor:   acceptAllAttestor{},
		Dispatcher: dispatcher,
	})
	server := httptest.NewServer(relayHandler)
	t.Cleanup(server.Close)
	registerRouteWithURL(t, repository, owner.Actor, server.URL, routeID, capability, deviceKey)
	service := NewService(repository, NewHTTPSender(server.Client()))
	finishedAt := time.Date(2026, time.August, 17, 10, 0, 0, 0, time.UTC)
	run := state.AgentRun{
		ID:         "0198a08d-1ca1-7c44-8a5b-4e2f6a91b201",
		ReminderID: "0198a08d-1ca1-75b1-9c42-7d0e5f31a8b2",
		Status:     state.AgentRunStatusFailed,
		FinishedAt: &finishedAt,
	}
	if err := service.NotifyRunFinished(context.Background(), run, "Secret run title"); err != nil {
		t.Fatalf("NotifyRunFinished() error = %v", err)
	}
	if len(dispatcher.notifications) != 1 {
		t.Fatalf("notifications = %#v", dispatcher.notifications)
	}
	notification := dispatcher.notifications[0]
	if notification.PushType != relay.PushTypeAlert || notification.CollapseID != run.ID {
		t.Fatalf("notification metadata = %#v", notification)
	}
	encodedPayload := string(notification.Payload)
	if strings.Contains(encodedPayload, "Secret run title") || strings.Contains(encodedPayload, "failed") {
		t.Fatalf("outer APS payload leaks plaintext or status: %s", encodedPayload)
	}
	var decoded struct {
		APS struct {
			Category string `json:"category"`
		} `json:"aps"`
		State struct {
			Envelope pushcrypto.Envelope `json:"envelope"`
		} `json:"state"`
	}
	if err := json.Unmarshal(notification.Payload, &decoded); err != nil {
		t.Fatalf("decode notification payload: %v", err)
	}
	if decoded.APS.Category != "STATE_RUN" {
		t.Fatalf("category = %q", decoded.APS.Category)
	}
	opened, err := pushcrypto.Open(deviceKey.Bytes(), decoded.State.Envelope, []byte(routeID))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	payload := map[string]any{}
	if err := json.Unmarshal(opened, &payload); err != nil {
		t.Fatalf("opened payload is not JSON: %v", err)
	}
	if payload["kind"] != "run_finished" || payload["status"] != "failed" || payload["title"] != "Secret run title" || payload["run_id"] != run.ID {
		t.Fatalf("opened payload = %#v", payload)
	}
}

type failingSender struct {
	attempts int
}

func (sender *failingSender) Send(context.Context, DeviceRoute, string, string, []byte) error {
	sender.attempts++
	return errors.New("relay unreachable")
}

func newRouteKey(t *testing.T) *ecdh.PrivateKey {
	t.Helper()
	key, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}
	return key
}

func registerRoute(t *testing.T, repository *Repository, actor state.Actor, routeID string, authorization string, key *ecdh.PrivateKey) {
	t.Helper()
	registerRouteWithURL(t, repository, actor, "https://relay.example.com", routeID, authorization, key)
}

func registerRouteWithURL(t *testing.T, repository *Repository, actor state.Actor, relayURL string, routeID string, authorization string, key *ecdh.PrivateKey) {
	t.Helper()
	if _, err := repository.RegisterDevice(context.Background(), actor, RegisterDeviceInput{
		RelayURL:      relayURL,
		RouteID:       routeID,
		Authorization: authorization,
		PublicKey:     key.PublicKey().Bytes(),
	}); err != nil {
		t.Fatalf("RegisterDevice() error = %v", err)
	}
}

type singleRouteRepository struct {
	route relay.Route
}

func (repository *singleRouteRepository) CreateChallenge(context.Context, relay.Challenge) error {
	return nil
}

func (repository *singleRouteRepository) ConsumeChallenge(context.Context, string, time.Time) error {
	return nil
}

func (repository *singleRouteRepository) CreateRoute(_ context.Context, route relay.Route) error {
	repository.route = route
	return nil
}

func (repository *singleRouteRepository) GetRoute(context.Context, string) (relay.Route, error) {
	return repository.route, nil
}

func (repository *singleRouteRepository) UpdateRouteToken(context.Context, string, string) error {
	return nil
}

func (repository *singleRouteRepository) DeleteRoute(context.Context, string) error {
	return nil
}

type acceptAllAttestor struct{}

func (acceptAllAttestor) Verify(context.Context, relay.AttestationInput) error {
	return nil
}

type recordingRelayDispatcher struct {
	notifications []relay.Notification
}

func (dispatcher *recordingRelayDispatcher) Send(_ context.Context, notification relay.Notification) error {
	dispatcher.notifications = append(dispatcher.notifications, notification)
	return nil
}

type recordedDelivery struct {
	route      DeviceRoute
	kind       string
	collapseID string
	plaintext  []byte
}

type recordingSender struct {
	deliveries []recordedDelivery
}

func (sender *recordingSender) Send(_ context.Context, route DeviceRoute, kind string, collapseID string, plaintext []byte) error {
	sender.deliveries = append(sender.deliveries, recordedDelivery{
		route:      route,
		kind:       kind,
		collapseID: collapseID,
		plaintext:  append([]byte(nil), plaintext...),
	})
	return nil
}

func newPushTestApplication(t *testing.T) (*pocketbase.PocketBase, *state.Service, stateauth.Credential) {
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
	t.Cleanup(func() { _ = app.ResetBootstrapState() })
	seed := make([]byte, ed25519.SeedSize)
	for index := range seed {
		seed[index] = byte(index + 21)
	}
	stateRepository, err := store.NewPocketBaseRepository(app, ed25519.NewKeyFromSeed(seed))
	if err != nil {
		t.Fatalf("NewPocketBaseRepository() error = %v", err)
	}
	authManager, err := stateauth.NewManager(app, "bootstrap-secret")
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	owner, err := authManager.BootstrapOwner(context.Background(), "bootstrap-secret", stateauth.OwnerBootstrapRequest{
		DisplayName: "Fabian",
		DeviceName:  "iPhone",
	})
	if err != nil {
		t.Fatalf("BootstrapOwner() error = %v", err)
	}
	return app, state.NewService(stateRepository, state.WithClock(func() time.Time {
		return time.Date(2026, time.August, 11, 20, 0, 0, 0, time.UTC)
	})), owner
}
