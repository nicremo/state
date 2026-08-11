package relay

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestAPNSDispatcherSendsTokenAuthenticatedNotification(t *testing.T) {
	t.Parallel()

	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}
	var receivedPath string
	var receivedHeaders http.Header
	var receivedPayload []byte
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		receivedPath = request.URL.Path
		receivedHeaders = request.Header.Clone()
		receivedPayload, _ = io.ReadAll(request.Body)
		writer.Header().Set("apns-id", "apns-request-id")
		writer.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)
	now := time.Date(2026, time.August, 11, 20, 0, 0, 0, time.UTC)
	dispatcher, err := NewAPNSDispatcher(APNSConfig{
		TeamID:        "5DKU7FFK4X",
		KeyID:         "ASCKEYID000",
		Topic:         "com.fabincrm.state",
		PrivateKey:    privateKey,
		HTTPClient:    server.Client(),
		ProductionURL: server.URL,
		SandboxURL:    server.URL,
		Clock:         func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("NewAPNSDispatcher() error = %v", err)
	}
	payload := json.RawMessage(`{"aps":{"alert":{"title":"State"}}}`)
	token := strings.Repeat("a", 64)
	if err := dispatcher.Send(context.Background(), Notification{
		APNSToken:   token,
		Environment: EnvironmentSandbox,
		PushType:    PushTypeAlert,
		CollapseID:  "occurrence-id",
		Payload:     payload,
	}); err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if receivedPath != "/3/device/"+token {
		t.Fatalf("path = %q", receivedPath)
	}
	if !strings.HasPrefix(receivedHeaders.Get("Authorization"), "bearer ") {
		t.Fatalf("authorization = %q", receivedHeaders.Get("Authorization"))
	}
	if receivedHeaders.Get("apns-topic") != "com.fabincrm.state" || receivedHeaders.Get("apns-push-type") != "alert" || receivedHeaders.Get("apns-priority") != "10" {
		t.Fatalf("headers = %#v", receivedHeaders)
	}
	if receivedHeaders.Get("apns-collapse-id") != "occurrence-id" || string(receivedPayload) != string(payload) {
		t.Fatalf("payload metadata = %q, %s", receivedHeaders.Get("apns-collapse-id"), receivedPayload)
	}
}

func TestAPNSDispatcherReturnsTypedFailure(t *testing.T) {
	t.Parallel()

	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusGone)
		_ = json.NewEncoder(writer).Encode(map[string]string{"reason": "Unregistered"})
	}))
	t.Cleanup(server.Close)
	dispatcher, err := NewAPNSDispatcher(APNSConfig{
		TeamID:        "5DKU7FFK4X",
		KeyID:         "ASCKEYID000",
		Topic:         "com.fabincrm.state",
		PrivateKey:    privateKey,
		HTTPClient:    server.Client(),
		ProductionURL: server.URL,
		SandboxURL:    server.URL,
	})
	if err != nil {
		t.Fatalf("NewAPNSDispatcher() error = %v", err)
	}
	err = dispatcher.Send(context.Background(), Notification{
		APNSToken:   strings.Repeat("b", 64),
		Environment: EnvironmentProduction,
		PushType:    PushTypeBackground,
		Payload:     []byte(`{"aps":{"content-available":1}}`),
	})
	if err == nil || !strings.Contains(err.Error(), "Unregistered") {
		t.Fatalf("Send() error = %v", err)
	}
}
