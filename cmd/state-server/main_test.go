package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/nicremo/state/internal/state"
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
	mcpRequest := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	mcpResponse := httptest.NewRecorder()
	application.handler.ServeHTTP(mcpResponse, mcpRequest)
	if mcpResponse.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated MCP status = %d, body = %s", mcpResponse.Code, mcpResponse.Body.String())
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

// The execution scheduler turns a due occurrence with an enabled policy into a
// claimable run on the real application assembly.
func TestExecutionSchedulerMaterializesClaimableRuns(t *testing.T) {
	t.Parallel()

	dataDirectory := t.TempDir()
	application, err := newApplication(applicationConfig{
		dataDirectory: dataDirectory,
		version:       "test-version",
	})
	if err != nil {
		t.Fatalf("newApplication() error = %v", err)
	}
	t.Cleanup(func() {
		if err := application.close(); err != nil {
			t.Errorf("close() error = %v", err)
		}
	})

	bootstrapToken, err := os.ReadFile(filepath.Join(dataDirectory, "state_secrets", "bootstrap.token"))
	if err != nil {
		t.Fatalf("read bootstrap token: %v", err)
	}
	owner := serveJSON(t, application.handler, http.MethodPost, "/api/v1/pairing/owner", "", map[string]any{
		"display_name": "Fabian",
		"device_name":  "iPhone",
	}, string(bytes.TrimSpace(bootstrapToken)), http.StatusCreated)
	ownerToken, _ := owner["token"].(string)
	if ownerToken == "" {
		t.Fatalf("owner credential = %#v", owner)
	}

	project := serveJSON(t, application.handler, http.MethodPost, "/api/v1/projects", ownerToken, map[string]any{
		"name":              "customer-api",
		"client_request_id": "0198a5e0-0000-7000-8000-000000000001",
	}, "", http.StatusCreated)
	policy := serveJSON(t, application.handler, http.MethodPost, "/api/v1/policies", ownerToken, map[string]any{
		"name":                 "nightly-review",
		"project_id":           project["id"],
		"adapter":              "codex",
		"mode":                 "supervised",
		"allowed_capabilities": []string{"read_repository", "run_tests"},
		"timeout_minutes":      30,
		"client_request_id":    "0198a5e0-0000-7000-8000-000000000002",
	}, "", http.StatusCreated)

	yesterday := time.Now().UTC().Add(-26 * time.Hour)
	reminder := serveJSON(t, application.handler, http.MethodPost, "/api/v1/reminders", ownerToken, map[string]any{
		"title":               "Review the nightly metrics",
		"client_request_id":   "0198a5e0-0000-7000-8000-000000000003",
		"execution_policy_id": policy["id"],
		"schedule": map[string]any{
			"local_date": yesterday.Format("2006-01-02"),
			"local_time": yesterday.Format("15:04"),
			"time_zone":  "UTC",
			"mode":       "fixed",
		},
	}, "", http.StatusCreated)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	runExecutionCycle(context.Background(), application.state, logger, time.Now().UTC())

	runs := serveJSON(t, application.handler, http.MethodGet, "/api/v1/runs?reminder_id="+reminder["id"].(string), ownerToken, nil, "", http.StatusOK)
	runList, _ := runs["runs"].([]any)
	if len(runList) != 1 {
		t.Fatalf("materialized runs = %#v", runs)
	}
	run, _ := runList[0].(map[string]any)
	if run["status"] != string(state.AgentRunStatusEligible) {
		t.Fatalf("materialized run = %#v", run)
	}

	// A repeated cycle stays idempotent.
	runExecutionCycle(context.Background(), application.state, logger, time.Now().UTC())
	again := serveJSON(t, application.handler, http.MethodGet, "/api/v1/runs?reminder_id="+reminder["id"].(string), ownerToken, nil, "", http.StatusOK)
	if againRuns, _ := again["runs"].([]any); len(againRuns) != 1 {
		t.Fatalf("runs after second cycle = %#v", again)
	}

	pairing := serveJSON(t, application.handler, http.MethodPost, "/api/v1/pairing/codes", ownerToken, map[string]any{
		"kind":         "runner",
		"display_name": "Mac mini",
	}, "", http.StatusCreated)
	credential := serveJSON(t, application.handler, http.MethodPost, "/api/v1/pairing/exchange", "", map[string]any{
		"code": pairing["code"],
	}, "", http.StatusCreated)
	runnerToken, _ := credential["token"].(string)
	serveJSON(t, application.handler, http.MethodPost, "/api/v1/runner/registration", runnerToken, map[string]any{
		"display_name":      "Mac mini",
		"projects":          []string{project["id"].(string)},
		"adapters":          []string{"codex"},
		"client_request_id": "0198a5e0-0000-7000-8000-000000000004",
	}, "", http.StatusCreated)

	claimed := serveJSON(t, application.handler, http.MethodPost, "/api/v1/runner/claims", runnerToken, nil, "", http.StatusOK)
	if claimed["id"] != run["id"] || claimed["status"] != string(state.AgentRunStatusClaimed) {
		t.Fatalf("claimed run = %#v", claimed)
	}
}

// serveJSON performs one JSON request against the application handler and
// decodes the expected response.
func serveJSON(t *testing.T, handler http.Handler, method string, path string, token string, input any, bootstrapToken string, wantStatus int) map[string]any {
	t.Helper()

	var body bytes.Buffer
	if input != nil {
		if err := json.NewEncoder(&body).Encode(input); err != nil {
			t.Fatalf("encode request: %v", err)
		}
	}
	request := httptest.NewRequest(method, path, &body)
	request.Header.Set("Content-Type", "application/json")
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	if bootstrapToken != "" {
		request.Header.Set("X-State-Bootstrap-Token", bootstrapToken)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != wantStatus {
		t.Fatalf("%s %s status = %d, want %d, body = %s", method, path, response.Code, wantStatus, response.Body.String())
	}
	decoded := map[string]any{}
	if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("decode response %q: %v", response.Body.String(), err)
	}
	return decoded
}
