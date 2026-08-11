package statectl

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	stateauth "github.com/nicremo/state/internal/auth"
	"github.com/nicremo/state/internal/state"
)

type PairRequest struct {
	ProfileName string
	ServerURL   string
	Code        string
	Harness     string
}

type PairService struct {
	config     *ConfigStore
	secrets    SecretStore
	httpClient *http.Client
}

func NewPairService(config *ConfigStore, secrets SecretStore, httpClient *http.Client) *PairService {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 20 * time.Second}
	}
	return &PairService{config: config, secrets: secrets, httpClient: httpClient}
}

func (service *PairService) Pair(ctx context.Context, request PairRequest) (Profile, error) {
	if service.config == nil || service.secrets == nil || request.ProfileName == "" || request.Code == "" || !validHarness(request.Harness) {
		return Profile{}, state.ErrInvalidInput
	}
	serverURL := strings.TrimRight(strings.TrimSpace(request.ServerURL), "/")
	probe := Profile{
		Name:      request.ProfileName,
		ServerURL: serverURL,
		ActorID:   "pending",
		Harness:   request.Harness,
	}
	if err := validateProfile(probe); err != nil {
		return Profile{}, err
	}
	body, err := json.Marshal(map[string]string{"code": request.Code})
	if err != nil {
		return Profile{}, err
	}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, serverURL+"/api/v1/pairing/exchange", bytes.NewReader(body))
	if err != nil {
		return Profile{}, fmt.Errorf("create pairing request: %w", err)
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	httpResponse, err := service.httpClient.Do(httpRequest)
	if err != nil {
		return Profile{}, fmt.Errorf("exchange pairing code: %w", err)
	}
	defer httpResponse.Body.Close()
	limited := io.LimitReader(httpResponse.Body, 1<<20)
	if httpResponse.StatusCode != http.StatusCreated {
		var responseError struct {
			Code string `json:"code"`
		}
		_ = json.NewDecoder(limited).Decode(&responseError)
		if responseError.Code == "" {
			responseError.Code = "pairing_failed"
		}
		return Profile{}, fmt.Errorf("pairing failed with status %d and code %s", httpResponse.StatusCode, responseError.Code)
	}
	var credential stateauth.Credential
	if err := json.NewDecoder(limited).Decode(&credential); err != nil {
		return Profile{}, fmt.Errorf("decode pairing response: %w", err)
	}
	if credential.Token == "" || credential.Actor.ID == "" || credential.Actor.Kind != state.ActorKindHarness || credential.Actor.Harness != request.Harness {
		return Profile{}, errors.New("pairing response does not match requested harness")
	}
	profile := Profile{
		Name:      request.ProfileName,
		ServerURL: serverURL,
		ActorID:   credential.Actor.ID,
		Harness:   credential.Actor.Harness,
	}
	account := profile.CredentialAccount()
	if err := service.secrets.Set(account, credential.Token); err != nil {
		return Profile{}, fmt.Errorf("store credential in operating system keychain: %w", err)
	}
	if err := service.config.SaveProfile(profile); err != nil {
		_ = service.secrets.Delete(account)
		return Profile{}, err
	}
	return profile, nil
}

func validHarness(harness string) bool {
	switch harness {
	case "codex", "claude-code", "opencode":
		return true
	default:
		return false
	}
}
