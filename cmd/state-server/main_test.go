package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestNewApplicationCreatesPrivatePersistentSecretsAndServesHealth(t *testing.T) {
	t.Parallel()

	dataDirectory := t.TempDir()
	application, err := newApplication(applicationConfig{
		dataDirectory: dataDirectory,
		version:       "test-version",
	})
	if err != nil {
		t.Fatalf("newApplication() error = %v", err)
	}
	request := httptest.NewRequest(http.MethodGet, "/health/ready", nil)
	response := httptest.NewRecorder()
	application.handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("ready status = %d, body = %s", response.Code, response.Body.String())
	}
	if err := application.close(); err != nil {
		t.Fatalf("close() error = %v", err)
	}

	for _, name := range []string{"audit-signing.key", "bootstrap.token"} {
		path := filepath.Join(dataDirectory, "state_secrets", name)
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("Stat(%s) error = %v", name, err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("%s permissions = %o, want 600", name, info.Mode().Perm())
		}
	}

	restarted, err := newApplication(applicationConfig{
		dataDirectory: dataDirectory,
		version:       "test-version",
	})
	if err != nil {
		t.Fatalf("newApplication() after restart error = %v", err)
	}
	if err := restarted.close(); err != nil {
		t.Fatalf("second close() error = %v", err)
	}
}
