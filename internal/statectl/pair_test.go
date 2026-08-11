package statectl

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	stateauth "github.com/nicremo/state/internal/auth"
	"github.com/nicremo/state/internal/state"
)

func TestPairStoresProfileAndCredentialSeparately(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/api/v1/pairing/exchange" || request.Method != http.MethodPost {
			http.NotFound(writer, request)
			return
		}
		input := struct {
			Code string `json:"code"`
		}{}
		if err := json.NewDecoder(request.Body).Decode(&input); err != nil || input.Code != "ABCDE-23456" {
			http.Error(writer, "invalid code", http.StatusBadRequest)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(writer).Encode(stateauth.Credential{
			Actor: state.Actor{
				ID:          "01989f4a-ddfa-73a5-a131-3a6ef6a09cba",
				Kind:        state.ActorKindHarness,
				DisplayName: "Claude Code",
				Harness:     "claude-code",
				DeviceName:  "MacBook",
			},
			Token: "state_secret_credential",
		})
	}))
	t.Cleanup(server.Close)

	configStore := NewConfigStore(filepath.Join(t.TempDir(), "statectl.json"))
	secrets := &memorySecretStore{values: make(map[string]string)}
	pairer := NewPairService(configStore, secrets, server.Client())
	profile, err := pairer.Pair(context.Background(), PairRequest{
		ProfileName: "claude-code",
		ServerURL:   server.URL,
		Code:        "ABCDE-23456",
		Harness:     "claude-code",
	})
	if err != nil {
		t.Fatalf("Pair() error = %v", err)
	}
	loaded, err := configStore.LoadProfile("claude-code")
	if err != nil || loaded != profile {
		t.Fatalf("LoadProfile() = %#v, %v", loaded, err)
	}
	credential, err := secrets.Get(profile.CredentialAccount())
	if err != nil {
		t.Fatalf("secret Get() error = %v", err)
	}
	if credential != "state_secret_credential" {
		t.Fatalf("credential = %q", credential)
	}
}

func TestPairRejectsMismatchedHarness(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(writer).Encode(stateauth.Credential{
			Actor: state.Actor{ID: "01989f4a-ddfa-769f-bd09-53052672c44f", Kind: state.ActorKindHarness, Harness: "codex"},
			Token: "state_secret_credential",
		})
	}))
	t.Cleanup(server.Close)

	pairer := NewPairService(NewConfigStore(filepath.Join(t.TempDir(), "statectl.json")), &memorySecretStore{values: make(map[string]string)}, server.Client())
	_, err := pairer.Pair(context.Background(), PairRequest{
		ProfileName: "claude-code",
		ServerURL:   server.URL,
		Code:        "ABCDE-23456",
		Harness:     "claude-code",
	})
	if err == nil {
		t.Fatal("Pair() succeeded with mismatched harness")
	}
}

type memorySecretStore struct {
	values map[string]string
}

func (store *memorySecretStore) Set(account string, value string) error {
	store.values[account] = value
	return nil
}

func (store *memorySecretStore) Get(account string) (string, error) {
	value, ok := store.values[account]
	if !ok {
		return "", ErrCredentialNotFound
	}
	return value, nil
}

func (store *memorySecretStore) Delete(account string) error {
	delete(store.values, account)
	return nil
}
