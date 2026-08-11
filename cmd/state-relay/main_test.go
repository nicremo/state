package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNewRelayApplicationIsReadyInExplicitDryRunMode(t *testing.T) {
	t.Parallel()

	application, err := newRelayApplication(relayConfig{
		dataDirectory: t.TempDir(),
		appID:         "5DKU7FFK4X.com.fabincrm.state",
		dryRunAPNS:    true,
		version:       "test-version",
	})
	if err != nil {
		t.Fatalf("newRelayApplication() error = %v", err)
	}
	t.Cleanup(func() { _ = application.close() })
	request := httptest.NewRequest(http.MethodGet, "/health/ready", nil)
	response := httptest.NewRecorder()
	application.handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("ready status = %d, body = %s", response.Code, response.Body.String())
	}
}

func TestNewRelayApplicationRequiresAPNSConfiguration(t *testing.T) {
	t.Parallel()

	_, err := newRelayApplication(relayConfig{
		dataDirectory: t.TempDir(),
		appID:         "5DKU7FFK4X.com.fabincrm.state",
		version:       "test-version",
	})
	if err == nil {
		t.Fatal("newRelayApplication() succeeded without APNs credentials")
	}
}
