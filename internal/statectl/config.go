package statectl

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

var ErrProfileNotFound = errors.New("statectl profile not found")

type Profile struct {
	Name      string `json:"name"`
	ServerURL string `json:"server_url"`
	ActorID   string `json:"actor_id"`
	Harness   string `json:"harness"`
}

type configFile struct {
	Version  int                `json:"version"`
	Profiles map[string]Profile `json:"profiles"`
}

type ConfigStore struct {
	path string
}

func NewConfigStore(path string) *ConfigStore {
	return &ConfigStore{path: path}
}

func DefaultConfigPath() (string, error) {
	directory, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(directory, "State", "statectl.json"), nil
}

func (store *ConfigStore) SaveProfile(profile Profile) error {
	if err := validateProfile(profile); err != nil {
		return err
	}
	config, err := store.load()
	if err != nil {
		return err
	}
	config.Profiles[profile.Name] = profile
	return store.save(config)
}

func (store *ConfigStore) LoadProfile(name string) (Profile, error) {
	config, err := store.load()
	if err != nil {
		return Profile{}, err
	}
	profile, ok := config.Profiles[name]
	if !ok {
		return Profile{}, ErrProfileNotFound
	}
	return profile, nil
}

func (store *ConfigStore) RemoveProfile(name string) error {
	config, err := store.load()
	if err != nil {
		return err
	}
	if _, ok := config.Profiles[name]; !ok {
		return ErrProfileNotFound
	}
	delete(config.Profiles, name)
	return store.save(config)
}

func (store *ConfigStore) load() (configFile, error) {
	config := configFile{Version: 1, Profiles: make(map[string]Profile)}
	contents, err := os.ReadFile(store.path)
	if errors.Is(err, os.ErrNotExist) {
		return config, nil
	}
	if err != nil {
		return configFile{}, fmt.Errorf("read statectl config: %w", err)
	}
	if err := json.Unmarshal(contents, &config); err != nil {
		return configFile{}, fmt.Errorf("decode statectl config: %w", err)
	}
	if config.Version != 1 {
		return configFile{}, fmt.Errorf("unsupported statectl config version %d", config.Version)
	}
	if config.Profiles == nil {
		config.Profiles = make(map[string]Profile)
	}
	return config, nil
}

func (store *ConfigStore) save(config configFile) error {
	if store.path == "" {
		return errors.New("statectl config path is empty")
	}
	if err := os.MkdirAll(filepath.Dir(store.path), 0o700); err != nil {
		return fmt.Errorf("create statectl config directory: %w", err)
	}
	encoded, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return fmt.Errorf("encode statectl config: %w", err)
	}
	temporary, err := os.CreateTemp(filepath.Dir(store.path), ".statectl-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary statectl config: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("secure temporary statectl config: %w", err)
	}
	if _, err := temporary.Write(append(encoded, '\n')); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("write temporary statectl config: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("sync temporary statectl config: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close temporary statectl config: %w", err)
	}
	if err := os.Rename(temporaryPath, store.path); err != nil {
		return fmt.Errorf("replace statectl config: %w", err)
	}
	return os.Chmod(store.path, 0o600)
}

func (profile Profile) CredentialAccount() string {
	digest := sha256.Sum256([]byte(profile.ServerURL))
	return profile.Name + ":" + hex.EncodeToString(digest[:8])
}

func validateProfile(profile Profile) error {
	if profile.Name == "" || profile.ServerURL == "" || profile.ActorID == "" || profile.Harness == "" {
		return errors.New("statectl profile is incomplete")
	}
	return ValidateServerURL(profile.ServerURL)
}

// ValidateServerURL enforces the transport rule shared by statectl profiles
// and the runner configuration: HTTPS everywhere except loopback development
// servers.
func ValidateServerURL(serverURL string) error {
	parsed, err := url.Parse(serverURL)
	if err != nil || parsed.Host == "" {
		return errors.New("state server URL is invalid")
	}
	if parsed.Scheme != "https" && !(parsed.Scheme == "http" && isLoopbackHost(parsed.Hostname())) {
		return errors.New("state server URL must use HTTPS except on loopback")
	}
	return nil
}

func isLoopbackHost(host string) bool {
	host = strings.ToLower(host)
	return host == "localhost" || host == "127.0.0.1" || host == "::1"
}
