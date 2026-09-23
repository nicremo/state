package main

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

func TestRunVersion(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&stderr, nil))
	if err := run([]string{"version"}, &stdout, &stderr, logger); err != nil {
		t.Fatalf("run(version) error = %v", err)
	}
	if strings.TrimSpace(stdout.String()) != version {
		t.Fatalf("version output = %q, want %q", stdout.String(), version)
	}
}

func TestRunRejectsUnknownCommand(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer
	err := run([]string{"unknown"}, &output, &output, slog.New(slog.NewTextHandler(&output, nil)))
	if err == nil {
		t.Fatal("run(unknown) succeeded")
	}
}

func TestRunPairRequiresFlags(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer
	err := run([]string{"pair", "--server", "https://state.example.com"}, &output, &output, slog.New(slog.NewTextHandler(&output, nil)))
	if err == nil || !strings.Contains(err.Error(), "--server, --code, --name and --work-root") {
		t.Fatalf("run(pair) error = %v, want flag guidance", err)
	}
}

// Pairing used to build a config without poll intervals, which Validate
// rejects, so state-runner pair failed before it ever reached the server.
func TestPairConfigPassesValidation(t *testing.T) {
	config := newPairConfig(" http://127.0.0.1:9848/ ", " MacBook Pro ", "", "claude-code,codex", t.TempDir())
	if err := config.Validate(); err != nil {
		t.Fatalf("pair config does not validate: %v", err)
	}
	if config.ServerURL != "http://127.0.0.1:9848" || config.Name != "MacBook Pro" {
		t.Fatalf("pair config not normalized: %+v", config)
	}
	if len(config.Adapters) != 2 {
		t.Fatalf("adapters = %#v", config.Adapters)
	}
}
