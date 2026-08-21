package runner

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunnerConfigRoundtripAppliesDefaults(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "state", "runner.json")
	config := RunnerConfig{
		ServerURL: "https://state.example.com",
		Name:      "mac-mini",
		Projects:  []string{"01989f4a-ddfa-73a5-a131-3a6ef6a09cba"},
		Adapters:  []string{"codex"},
		WorkRoot:  "/srv/work",
	}
	if err := SaveRunnerConfig(path, config); err != nil {
		t.Fatalf("SaveRunnerConfig() error = %v", err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("config mode = %v, %v", info.Mode(), err)
	}
	loaded, err := LoadRunnerConfig(path)
	if err != nil {
		t.Fatalf("LoadRunnerConfig() error = %v", err)
	}
	if loaded.Version != 1 || loaded.PollIntervalSeconds != DefaultPollIntervalSeconds || loaded.LongPollSeconds != DefaultLongPollSeconds {
		t.Fatalf("loaded config = %#v", loaded)
	}
	if loaded.ServerURL != config.ServerURL || loaded.Name != config.Name || loaded.WorkRoot != config.WorkRoot {
		t.Fatalf("loaded config = %#v", loaded)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if strings.Contains(string(contents), "state_") || strings.Contains(string(contents), "token") {
		t.Fatalf("runner config contains credential material: %s", contents)
	}
	if config.CredentialAccount() == (RunnerConfig{Name: "studio", ServerURL: config.ServerURL}).CredentialAccount() {
		t.Fatal("credential accounts collide across runner names")
	}
}

func TestRunnerConfigValidation(t *testing.T) {
	t.Parallel()

	valid := RunnerConfig{
		ServerURL:           "https://state.example.com",
		Name:                "mac-mini",
		WorkRoot:            "/srv/work",
		PollIntervalSeconds: 5,
		LongPollSeconds:     25,
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}

	loopback := valid
	loopback.ServerURL = "http://127.0.0.1:8080"
	if err := loopback.Validate(); err != nil {
		t.Fatalf("Validate(loopback) error = %v", err)
	}

	cases := []RunnerConfig{
		{ServerURL: "", Name: "x", WorkRoot: "/srv", PollIntervalSeconds: 5, LongPollSeconds: 25},
		{ServerURL: "http://state.example.com", Name: "x", WorkRoot: "/srv", PollIntervalSeconds: 5, LongPollSeconds: 25},
		{ServerURL: "https://state.example.com", Name: "", WorkRoot: "/srv", PollIntervalSeconds: 5, LongPollSeconds: 25},
		{ServerURL: "https://state.example.com", Name: "x", WorkRoot: "", PollIntervalSeconds: 5, LongPollSeconds: 25},
		{ServerURL: "https://state.example.com", Name: "x", WorkRoot: "/srv", PollIntervalSeconds: 0, LongPollSeconds: 25},
		{ServerURL: "https://state.example.com", Name: "x", WorkRoot: "/srv", PollIntervalSeconds: 5, LongPollSeconds: 26},
	}
	for index, config := range cases {
		if err := config.Validate(); err == nil {
			t.Fatalf("Validate() case %d succeeded, want error (%#v)", index, config)
		}
	}
}

func TestLoadRunnerConfigGuidesPairing(t *testing.T) {
	t.Parallel()

	_, err := LoadRunnerConfig(filepath.Join(t.TempDir(), "runner.json"))
	if err == nil || !strings.Contains(err.Error(), "state-runner pair") {
		t.Fatalf("LoadRunnerConfig() error = %v, want pairing guidance", err)
	}
}

func TestLoadRunnerConfigRejectsUnknownVersion(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "runner.json")
	if err := os.WriteFile(path, []byte(`{"version": 99}`), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if _, err := LoadRunnerConfig(path); err == nil || !strings.Contains(err.Error(), "version") {
		t.Fatalf("LoadRunnerConfig() error = %v, want version error", err)
	}
}
