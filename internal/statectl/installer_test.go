package statectl

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestInstallerBacksUpAndPreservesCodexConfiguration(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	paths := InstallPaths{
		CodexConfig: filepath.Join(directory, "codex", "config.toml"),
		CodexRules:  filepath.Join(directory, "codex", "AGENTS.md"),
	}
	if err := os.MkdirAll(filepath.Dir(paths.CodexConfig), 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(paths.CodexConfig, []byte("model = \"gpt-5\"\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := os.WriteFile(paths.CodexRules, []byte("# Existing\n\nKeep me.\n"), 0o600); err != nil {
		t.Fatalf("write rules: %v", err)
	}
	installer := NewInstaller(paths, "/usr/local/bin/statectl", func() time.Time {
		return time.Date(2026, time.August, 11, 21, 30, 0, 0, time.UTC)
	})
	if err := installer.Install("codex", "codex"); err != nil {
		t.Fatalf("Install() error = %v", err)
	}
	if err := installer.Install("codex", "codex"); err != nil {
		t.Fatalf("second Install() error = %v", err)
	}
	config := readText(t, paths.CodexConfig)
	rules := readText(t, paths.CodexRules)
	if !strings.Contains(config, "model = \"gpt-5\"") || strings.Count(config, ConfigBlockStart) != 1 {
		t.Fatalf("installed config is invalid:\n%s", config)
	}
	if !strings.Contains(config, "/usr/local/bin/statectl") || strings.Contains(config, "state_secret") {
		t.Fatalf("installed config command is invalid:\n%s", config)
	}
	if !strings.Contains(rules, "Keep me.") || strings.Count(rules, RuleBlockStart) != 1 {
		t.Fatalf("installed rules are invalid:\n%s", rules)
	}
	backup := paths.CodexConfig + ".state-backup-20260811T213000Z"
	if _, err := os.Stat(backup); err != nil {
		t.Fatalf("backup Stat() error = %v", err)
	}

	if err := installer.Uninstall("codex"); err != nil {
		t.Fatalf("Uninstall() error = %v", err)
	}
	config = readText(t, paths.CodexConfig)
	rules = readText(t, paths.CodexRules)
	if strings.Contains(config, ConfigBlockStart) || !strings.Contains(config, "model = \"gpt-5\"") {
		t.Fatalf("uninstalled config is invalid:\n%s", config)
	}
	if strings.Contains(rules, RuleBlockStart) || !strings.Contains(rules, "Keep me.") {
		t.Fatalf("uninstalled rules are invalid:\n%s", rules)
	}
}

func TestInstallerWritesClaudeAndOpenCodeJSON(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	paths := InstallPaths{
		ClaudeConfig:   filepath.Join(directory, "claude.json"),
		ClaudeRules:    filepath.Join(directory, "claude", "CLAUDE.md"),
		OpenCodeConfig: filepath.Join(directory, "opencode", "opencode.json"),
		OpenCodeRules:  filepath.Join(directory, "opencode", "AGENTS.md"),
	}
	installer := NewInstaller(paths, "/opt/statectl", time.Now)
	if err := installer.Install("claude-code", "claude-code"); err != nil {
		t.Fatalf("Install(claude-code) error = %v", err)
	}
	if err := installer.Install("opencode", "opencode"); err != nil {
		t.Fatalf("Install(opencode) error = %v", err)
	}
	claude := readText(t, paths.ClaudeConfig)
	opencode := readText(t, paths.OpenCodeConfig)
	if !strings.Contains(claude, `"mcpServers"`) || !strings.Contains(claude, `"state"`) {
		t.Fatalf("Claude config is invalid:\n%s", claude)
	}
	if !strings.Contains(opencode, `"mcp"`) || !strings.Contains(opencode, `"type": "local"`) {
		t.Fatalf("OpenCode config is invalid:\n%s", opencode)
	}
}

func readText(t *testing.T, path string) string {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%s) error = %v", path, err)
	}
	return string(contents)
}
