package main

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestServiceRejectsUnknownSubcommand(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer
	err := run([]string{"service", "restart"}, &output, &output, slog.New(slog.NewTextHandler(&output, nil)))
	if err == nil {
		t.Fatal("run(service restart) succeeded")
	}
	if runtime.GOOS == "darwin" && !strings.Contains(err.Error(), "unknown service command") {
		t.Fatalf("run(service restart) error = %v, want unknown subcommand", err)
	}
}

func TestServiceRequiresASubcommand(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer
	err := run([]string{"service"}, &output, &output, slog.New(slog.NewTextHandler(&output, nil)))
	if err == nil {
		t.Fatal("run(service) succeeded")
	}
	if runtime.GOOS == "darwin" && !strings.Contains(err.Error(), "install|uninstall|status") {
		t.Fatalf("run(service) error = %v, want subcommand usage", err)
	}
}

func TestServiceInstallRejectsUnknownFlag(t *testing.T) {
	t.Parallel()

	if runtime.GOOS != "darwin" {
		t.Skip("launch agent management is darwin only")
	}
	var output bytes.Buffer
	err := run([]string{"service", "install", "--log-dir", "/tmp"}, &output, &output, slog.New(slog.NewTextHandler(&output, nil)))
	if err == nil || !strings.Contains(err.Error(), "log-dir") {
		t.Fatalf("run(service install --log-dir) error = %v, want flag error", err)
	}
}

func TestCheckServicePlatformOnlyAllowsDarwin(t *testing.T) {
	t.Parallel()

	if err := checkServicePlatform("darwin"); err != nil {
		t.Fatalf("checkServicePlatform(darwin) error = %v", err)
	}
	for _, goos := range []string{"linux", "windows"} {
		err := checkServicePlatform(goos)
		if err == nil {
			t.Fatalf("checkServicePlatform(%s) succeeded", goos)
		}
		want := "service management is only implemented for macOS; use systemd on Linux (see docs/runner-service.md)"
		if err.Error() != want {
			t.Fatalf("checkServicePlatform(%s) error = %q, want %q", goos, err, want)
		}
	}
}

func TestEnsureRunnerPairedRequiresConfig(t *testing.T) {
	t.Parallel()

	missing := filepath.Join(t.TempDir(), "runner.json")
	err := ensureRunnerPaired(missing)
	if err == nil || !strings.Contains(err.Error(), "runner is not paired yet, run state-runner pair first") {
		t.Fatalf("ensureRunnerPaired(missing) error = %v", err)
	}

	present := filepath.Join(t.TempDir(), "runner.json")
	if err := os.WriteFile(present, []byte("{}"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := ensureRunnerPaired(present); err != nil {
		t.Fatalf("ensureRunnerPaired(existing) error = %v", err)
	}
}

func TestDefaultAgentPathAddsToolDirectories(t *testing.T) {
	t.Parallel()

	got := defaultAgentPath("/usr/bin:/usr/local/bin:/usr/bin", "/Users/runner")
	want := "/usr/bin:/usr/local/bin:/Users/runner/.local/bin:/opt/homebrew/bin"
	if got != want {
		t.Fatalf("defaultAgentPath() = %q, want %q", got, want)
	}

	got = defaultAgentPath("", "/Users/runner")
	want = "/Users/runner/.local/bin:/opt/homebrew/bin:/usr/local/bin"
	if got != want {
		t.Fatalf("defaultAgentPath(empty) = %q, want %q", got, want)
	}
}
