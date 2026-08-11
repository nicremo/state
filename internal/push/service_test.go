package push

import (
	"bytes"
	"context"
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	stateauth "github.com/nicremo/state/internal/auth"
	"github.com/nicremo/state/internal/pushcrypto"
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
	start := time.Date(2026, time.August, 17, 8, 59, 0, 0, time.Local)
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
