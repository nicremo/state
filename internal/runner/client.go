package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/nicremo/state/internal/state"
)

// ErrUnauthorized marks a rejected runner credential (revoked or invalid).
var ErrUnauthorized = errors.New("unauthorized")

// APIError is an unexpected server answer that maps to no sentinel.
type APIError struct {
	Status int
	Code   string
}

func (err *APIError) Error() string {
	return fmt.Sprintf("request failed with status %d and code %s", err.Status, err.Code)
}

// Client is the runner's minimal REST client. It deliberately carries no MCP
// dependency: the runner lifecycle speaks plain REST, and only a launched
// harness would use MCP (through its own statectl proxy).
type Client struct {
	baseURL    string
	token      string
	httpClient *http.Client
}

func NewClient(serverURL string, token string, httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 20 * time.Second}
	}
	return &Client{
		baseURL:    strings.TrimRight(serverURL, "/"),
		token:      token,
		httpClient: httpClient,
	}
}

// pairCredential is the pairing exchange response, decoded locally so the
// runner binary never imports the auth (PocketBase) package.
type pairCredential struct {
	Actor state.Actor `json:"actor"`
	Token string      `json:"token"`
}

// ExchangePairingCode trades a one-time pairing code for a credential. The
// exchange endpoint is unauthenticated; the code itself is the secret.
func (client *Client) ExchangePairingCode(ctx context.Context, code string) (pairCredential, error) {
	var credential pairCredential
	if err := client.do(ctx, http.MethodPost, "/api/v1/pairing/exchange", map[string]string{"code": code}, "", http.StatusCreated, &credential); err != nil {
		return pairCredential{}, err
	}
	return credential, nil
}

// Register upserts this runner's profile (projects and adapters it serves).
func (client *Client) Register(ctx context.Context, input state.RegisterRunnerInput) (state.Runner, error) {
	var registered state.Runner
	if err := client.do(ctx, http.MethodPost, "/api/v1/runner/registration", input, client.token, http.StatusCreated, &registered); err != nil {
		return state.Runner{}, err
	}
	return registered, nil
}

// Claim long-polls for an eligible run. A 409 not_claimable maps to
// state.ErrNotClaimable ("no work"). The request deadline is wait + 10 s.
func (client *Client) Claim(ctx context.Context, waitSeconds int) (state.AgentRun, error) {
	if waitSeconds < 0 {
		waitSeconds = 0
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(waitSeconds+10)*time.Second)
	defer cancel()
	var run state.AgentRun
	err := client.do(ctx, http.MethodPost, "/api/v1/runner/claims?wait_seconds="+strconv.Itoa(waitSeconds), map[string]any{}, client.token, http.StatusOK, &run)
	if err != nil {
		return state.AgentRun{}, err
	}
	return run, nil
}

// ReportEvent sends started/progress/heartbeat and returns the current run.
func (client *Client) ReportEvent(ctx context.Context, runID string, event string, detail string, expectedRevision int64) (state.AgentRun, error) {
	var run state.AgentRun
	body := state.ReportRunEventInput{Event: event, Detail: detail, ExpectedRevision: expectedRevision}
	if err := client.do(ctx, http.MethodPost, "/api/v1/runner/runs/"+runID+"/events", body, client.token, http.StatusOK, &run); err != nil {
		return state.AgentRun{}, err
	}
	return run, nil
}

// Complete finalizes a run with the outcome, exit code and redacted summary.
func (client *Client) Complete(ctx context.Context, input state.CompleteRunInput) (state.AgentRun, error) {
	var run state.AgentRun
	if err := client.do(ctx, http.MethodPost, "/api/v1/runner/runs/"+input.RunID+"/complete", input, client.token, http.StatusOK, &run); err != nil {
		return state.AgentRun{}, err
	}
	return run, nil
}

// RequestApproval moves a running run into needs_approval for one capability.
func (client *Client) RequestApproval(ctx context.Context, input state.RequestApprovalInput) (state.AgentRun, error) {
	var run state.AgentRun
	if err := client.do(ctx, http.MethodPost, "/api/v1/runner/runs/"+input.RunID+"/approval", input, client.token, http.StatusOK, &run); err != nil {
		return state.AgentRun{}, err
	}
	return run, nil
}

// Briefing fetches the bounded, server-redacted briefing used to refresh
// .state/context/current.md before a launch.
func (client *Client) Briefing(ctx context.Context, limit int) (state.Briefing, error) {
	if limit <= 0 {
		limit = 50
	}
	var briefing state.Briefing
	if err := client.do(ctx, http.MethodGet, "/api/v1/briefing?limit="+strconv.Itoa(limit), nil, client.token, http.StatusOK, &briefing); err != nil {
		return state.Briefing{}, err
	}
	return briefing, nil
}

// do executes one JSON request. A non-expected status is decoded into the
// matching sentinel from internal/state where one exists.
func (client *Client) do(ctx context.Context, method string, path string, body any, token string, wantStatus int, out any) error {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, client.baseURL+path, reader)
	if err != nil {
		return err
	}
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := client.httpClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	limited := io.LimitReader(response.Body, 1<<20)
	if response.StatusCode != wantStatus {
		return decodeErrorResponse(limited, response.StatusCode)
	}
	if out == nil {
		io.Copy(io.Discard, limited)
		return nil
	}
	if err := json.NewDecoder(limited).Decode(out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}

func decodeErrorResponse(reader io.Reader, status int) error {
	var responseError struct {
		Code string `json:"code"`
	}
	_ = json.NewDecoder(reader).Decode(&responseError)
	switch responseError.Code {
	case "not_claimable":
		return state.ErrNotClaimable
	case "run_state_conflict":
		return state.ErrRunStateConflict
	case "revision_conflict":
		return state.ErrRevisionConflict
	case "forbidden", "policy_violation":
		return state.ErrForbidden
	case "invalid_input":
		return state.ErrInvalidInput
	case "not_found":
		return state.ErrNotFound
	case "unauthorized":
		return ErrUnauthorized
	}
	code := responseError.Code
	if code == "" {
		code = "request_failed"
	}
	return &APIError{Status: status, Code: code}
}
