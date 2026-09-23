package statectl

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
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
	if !strings.Contains(rules, DefaultAgentRules()) {
		t.Fatal("installed rules do not carry the current default agent rules")
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

func TestInstallerReportsManualInstallationForUnknownHarness(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	paths := InstallPaths{
		CodexConfig: filepath.Join(directory, "codex", "config.toml"),
		CodexRules:  filepath.Join(directory, "codex", "AGENTS.md"),
	}
	installer := NewInstaller(paths, "/usr/local/bin/statectl", nil)

	if err := installer.Install("pi", "pi"); !errors.Is(err, ErrManualInstallation) {
		t.Fatalf("Install() error = %v, want ErrManualInstallation", err)
	}
	if err := installer.Uninstall("pi"); !errors.Is(err, ErrManualInstallation) {
		t.Fatalf("Uninstall() error = %v, want ErrManualInstallation", err)
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatalf("ReadDir() error = %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("unknown harness must not create files, found %d entries", len(entries))
	}

	instructions := installer.ManualInstructions("pi", "pi")
	for _, fragment := range []string{"pi", "/usr/local/bin/statectl", "\"mcp\"", "--profile"} {
		if !strings.Contains(instructions, fragment) {
			t.Fatalf("manual instructions miss %q:\n%s", fragment, instructions)
		}
	}
}

func TestManualInstructionsForPiMentionPiSpecifics(t *testing.T) {
	t.Parallel()

	installer := NewInstaller(InstallPaths{}, "/usr/local/bin/statectl", nil)
	text := installer.ManualInstructions("pi-agent", "pi-main")
	// encodeJSONObject indents the args array, so the expected fragments follow
	// the real output instead of one imagined single line.
	for _, required := range []string{
		"Pi Agent:",
		"\"mcp\"",
		"\"--profile\"",
		"\"pi-main\"",
		"doctor --profile pi-main",
		DefaultAgentRules(),
	} {
		if !strings.Contains(text, required) {
			t.Errorf("manual instructions miss %q", required)
		}
	}
	if args := manualServerArgs(t, text); !reflect.DeepEqual(args, []string{"mcp", "--profile", "pi-main"}) {
		t.Errorf("MCP server args = %v, want [mcp --profile pi-main]", args)
	}
}

func TestManualInstructionsForPiAliasAndDeepSeekHarness(t *testing.T) {
	t.Parallel()

	installer := NewInstaller(InstallPaths{}, "/usr/local/bin/statectl", nil)
	if !strings.Contains(installer.ManualInstructions("pi", "pi"), "Pi Agent:") {
		t.Fatal("the pi alias must get the Pi Agent hint")
	}
	text := installer.ManualInstructions("deepseek-harness", "deepseek")
	if !strings.Contains(text, "DeepSeek Harness:") {
		t.Fatal("missing DeepSeek Harness hint")
	}
	if !strings.Contains(text, "doctor --profile deepseek") {
		t.Fatal("missing verification line for the DeepSeek Harness profile")
	}
}

func TestManualInstructionsForUnknownHarnessHaveNoProductHint(t *testing.T) {
	t.Parallel()

	installer := NewInstaller(InstallPaths{}, "/usr/local/bin/statectl", nil)
	text := installer.ManualInstructions("my-agent", "my-agent")
	if strings.Contains(text, "Pi Agent:") || strings.Contains(text, "DeepSeek Harness:") {
		t.Fatal("unknown harness must not get product specific hints")
	}
	if !strings.Contains(text, "doctor --profile my-agent") {
		t.Fatal("unknown harness must still get the verification line")
	}
}

// manualServerArgs extracts the args array of the printed state MCP server.
func manualServerArgs(t *testing.T, text string) []string {
	t.Helper()

	start := strings.Index(text, "{")
	end := strings.LastIndex(text, "}")
	if start < 0 || end < start {
		t.Fatalf("manual instructions have no JSON definition:\n%s", text)
	}
	var definition struct {
		McpServers struct {
			State struct {
				Args []string `json:"args"`
			} `json:"state"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal([]byte(text[start:end+1]), &definition); err != nil {
		t.Fatalf("decode printed MCP definition: %v", err)
	}
	return definition.McpServers.State.Args
}

func TestInstallerRejectsInvalidHarnessLabels(t *testing.T) {
	t.Parallel()

	installer := NewInstaller(InstallPaths{}, "/usr/local/bin/statectl", nil)
	for _, harness := range []string{"", "Codex", "claude code", "codex_cli"} {
		err := installer.Install(harness, "profile")
		if err == nil || errors.Is(err, ErrManualInstallation) {
			t.Fatalf("Install(%q) error = %v, want a validation error", harness, err)
		}
	}
}
