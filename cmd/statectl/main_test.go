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
