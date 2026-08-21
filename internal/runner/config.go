package runner

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/nicremo/state/internal/statectl"
)

// RunnerConfig is the persisted configuration of one state-runner process.
type RunnerConfig struct {
	Version             int      `json:"version"`
	ServerURL           string   `json:"server_url"`
	Name                string   `json:"name"`
	Projects            []string `json:"projects"`
	Adapters            []string `json:"adapters"`
	WorkRoot            string   `json:"work_root"`
	PollIntervalSeconds int      `json:"poll_interval_seconds"`
	LongPollSeconds     int      `json:"long_poll_seconds"`
}

const (
	runnerConfigVersion = 1
	// DefaultPollIntervalSeconds paces claim retries after an empty answer.
	DefaultPollIntervalSeconds = 5
	// DefaultLongPollSeconds matches the server-side claim wait cap.
	DefaultLongPollSeconds = 25
	// maxLongPollSeconds is the server-side cap for claim long-polling.
	maxLongPollSeconds = 25
)

// DefaultConfigPath is ~/.config/state/runner.json on Linux and the platform
// user-config equivalent elsewhere (same directory family statectl uses).
func DefaultConfigPath() (string, error) {
	directory, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(directory, "State", "runner.json"), nil
}

// CredentialAccount is the keychain account of the runner token. It is
// distinct from statectl harness profile accounts.
func (config RunnerConfig) CredentialAccount() string {
	digest := sha256.Sum256([]byte(config.ServerURL))
	return "runner:" + config.Name + ":" + hex.EncodeToString(digest[:8])
}

// Validate enforces the invariants a runner needs before pairing or running.
func (config RunnerConfig) Validate() error {
	if config.ServerURL == "" || config.Name == "" || config.WorkRoot == "" {
		return errors.New("runner config requires server_url, name and work_root")
	}
	if err := statectl.ValidateServerURL(config.ServerURL); err != nil {
		return err
	}
	if config.PollIntervalSeconds < 1 {
		return errors.New("runner poll_interval_seconds must be at least 1")
	}
	if config.LongPollSeconds < 1 || config.LongPollSeconds > maxLongPollSeconds {
		return fmt.Errorf("runner long_poll_seconds must be within 1..%d", maxLongPollSeconds)
	}
	return nil
}

// applyDefaults fills zero-valued intervals with their defaults.
func (config RunnerConfig) applyDefaults() RunnerConfig {
	if config.PollIntervalSeconds == 0 {
		config.PollIntervalSeconds = DefaultPollIntervalSeconds
	}
	if config.LongPollSeconds == 0 {
		config.LongPollSeconds = DefaultLongPollSeconds
	}
	return config
}

// SaveRunnerConfig writes the config atomically with 0600 permissions inside a
// 0700 directory.
func SaveRunnerConfig(path string, config RunnerConfig) error {
	if path == "" {
		return errors.New("runner config path is empty")
	}
	config.Version = runnerConfigVersion
	config = config.applyDefaults()
	if err := config.Validate(); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create runner config directory: %w", err)
	}
	encoded, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return fmt.Errorf("encode runner config: %w", err)
	}
	if err := statectl.WriteAtomic(path, append(encoded, '\n'), 0o600); err != nil {
		return fmt.Errorf("write runner config: %w", err)
	}
	return nil
}

// LoadRunnerConfig reads and validates the runner config.
func LoadRunnerConfig(path string) (RunnerConfig, error) {
	contents, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return RunnerConfig{}, fmt.Errorf("no runner config at %s — run state-runner pair first", path)
	}
	if err != nil {
		return RunnerConfig{}, fmt.Errorf("read runner config: %w", err)
	}
	var config RunnerConfig
	if err := json.Unmarshal(contents, &config); err != nil {
		return RunnerConfig{}, fmt.Errorf("decode runner config: %w", err)
	}
	if config.Version != runnerConfigVersion {
		return RunnerConfig{}, fmt.Errorf("unsupported runner config version %d", config.Version)
	}
	config = config.applyDefaults()
	if err := config.Validate(); err != nil {
		return RunnerConfig{}, err
	}
	return config, nil
}
