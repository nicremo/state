package statectl

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/nicremo/state/internal/state"
)

const (
	ConfigBlockStart = "# statectl:state:start"
	ConfigBlockEnd   = "# statectl:state:end"
)

// ErrManualInstallation reports a valid harness that statectl does not know how
// to configure. Pairing still succeeds and the caller prints the MCP server
// definition so the operator can paste it into that agent's own configuration.
var ErrManualInstallation = errors.New("harness has no shipped configuration path")

type InstallPaths struct {
	CodexConfig    string
	CodexRules     string
	ClaudeConfig   string
	ClaudeRules    string
	OpenCodeConfig string
	OpenCodeRules  string
}

type Installer struct {
	paths      InstallPaths
	executable string
	clock      func() time.Time
}

func NewInstaller(paths InstallPaths, executable string, clock func() time.Time) *Installer {
	if clock == nil {
		clock = time.Now
	}
	return &Installer{paths: paths, executable: executable, clock: clock}
}

func DefaultInstallPaths() (InstallPaths, error) {
	homeDirectory, err := os.UserHomeDir()
	if err != nil {
		return InstallPaths{}, err
	}
	return InstallPaths{
		CodexConfig:    filepath.Join(homeDirectory, ".codex", "config.toml"),
		CodexRules:     filepath.Join(homeDirectory, ".codex", "AGENTS.md"),
		ClaudeConfig:   filepath.Join(homeDirectory, ".claude.json"),
		ClaudeRules:    filepath.Join(homeDirectory, ".claude", "CLAUDE.md"),
		OpenCodeConfig: filepath.Join(homeDirectory, ".config", "opencode", "opencode.json"),
		OpenCodeRules:  filepath.Join(homeDirectory, ".config", "opencode", "AGENTS.md"),
	}, nil
}

func (installer *Installer) Install(harness string, profile string) error {
	if installer.executable == "" || profile == "" || !state.ValidHarness(harness) {
		return errors.New("invalid harness installation")
	}
	if !state.KnownHarness(harness) {
		return fmt.Errorf("%s: %w", harness, ErrManualInstallation)
	}
	configPath, rulesPath := installer.pathsFor(harness)
	if configPath == "" || rulesPath == "" {
		return errors.New("harness paths are not configured")
	}
	existingConfig, err := readOptional(configPath)
	if err != nil {
		return err
	}
	var updatedConfig string
	switch harness {
	case "codex":
		updatedConfig = upsertCodexConfig(existingConfig, installer.executable, profile)
	case "claude-code":
		updatedConfig, err = upsertClaudeConfig(existingConfig, installer.executable, profile)
	case "opencode":
		updatedConfig, err = upsertOpenCodeConfig(existingConfig, installer.executable, profile)
	}
	if err != nil {
		return err
	}
	if err := installer.writeIfChanged(configPath, existingConfig, updatedConfig, 0o600); err != nil {
		return err
	}
	existingRules, err := readOptional(rulesPath)
	if err != nil {
		return err
	}
	updatedRules := UpsertRuleBlock(existingRules, DefaultAgentRules())
	return installer.writeIfChanged(rulesPath, existingRules, updatedRules, 0o600)
}

func (installer *Installer) Uninstall(harness string) error {
	if !state.ValidHarness(harness) {
		return errors.New("invalid harness")
	}
	if !state.KnownHarness(harness) {
		return fmt.Errorf("%s: %w", harness, ErrManualInstallation)
	}
	configPath, rulesPath := installer.pathsFor(harness)
	existingConfig, err := readOptional(configPath)
	if err != nil {
		return err
	}
	var updatedConfig string
	switch harness {
	case "codex":
		updatedConfig = removeConfigBlock(existingConfig)
	case "claude-code":
		updatedConfig, err = removeJSONIntegration(existingConfig, "mcpServers")
	case "opencode":
		updatedConfig, err = removeJSONIntegration(existingConfig, "mcp")
	}
	if err != nil {
		return err
	}
	if err := installer.writeIfChanged(configPath, existingConfig, updatedConfig, 0o600); err != nil {
		return err
	}
	existingRules, err := readOptional(rulesPath)
	if err != nil {
		return err
	}
	return installer.writeIfChanged(rulesPath, existingRules, RemoveRuleBlock(existingRules), 0o600)
}

// ManualInstructions renders the MCP server definition and the agent rules for
// a harness that statectl does not configure itself. The operator pastes the
// definition into that agent's own configuration file.
func (installer *Installer) ManualInstructions(harness string, profile string) string {
	command := installer.executable
	if command == "" {
		command = "statectl"
	}
	definition, err := encodeJSONObject(map[string]any{
		"mcpServers": map[string]any{
			"state": map[string]any{
				"command": command,
				"args":    []string{"mcp", "--profile", profile},
			},
		},
	})
	if err != nil {
		definition = ""
	}
	return strings.Join([]string{
		"statectl has no shipped configuration for " + harness + ".",
		"The credential is stored and the profile is ready. Add this MCP server",
		"to that agent yourself:",
		"",
		strings.TrimRight(definition, "\n"),
		"",
		"Agents that expect a command line instead of JSON use:",
		"",
		"  " + command + " mcp --profile " + profile,
		"",
		"Then add these rules to that agent's instruction file:",
		"",
		DefaultAgentRules(),
		"",
	}, "\n")
}

func (installer *Installer) pathsFor(harness string) (string, string) {
	switch harness {
	case "codex":
		return installer.paths.CodexConfig, installer.paths.CodexRules
	case "claude-code":
		return installer.paths.ClaudeConfig, installer.paths.ClaudeRules
	case "opencode":
		return installer.paths.OpenCodeConfig, installer.paths.OpenCodeRules
	default:
		return "", ""
	}
}

func (installer *Installer) writeIfChanged(path string, previous string, next string, defaultMode os.FileMode) error {
	if previous == next {
		return nil
	}
	if path == "" {
		return errors.New("installation path is empty")
	}
	if previous != "" {
		if err := installer.backup(path); err != nil {
			return err
		}
	}
	return writeAtomic(path, []byte(next), defaultMode)
}

func (installer *Installer) backup(path string) error {
	suffix := installer.clock().UTC().Format("20060102T150405Z")
	backupPath := path + ".state-backup-" + suffix
	for index := 1; ; index++ {
		if _, err := os.Stat(backupPath); errors.Is(err, os.ErrNotExist) {
			break
		} else if err != nil {
			return err
		}
		backupPath = path + ".state-backup-" + suffix + "." + strconv.Itoa(index)
	}
	source, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open integration file for backup: %w", err)
	}
	defer source.Close()
	info, err := source.Stat()
	if err != nil {
		return err
	}
	destination, err := os.OpenFile(backupPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, info.Mode().Perm())
	if err != nil {
		return fmt.Errorf("create integration backup: %w", err)
	}
	_, copyErr := io.Copy(destination, source)
	closeErr := destination.Close()
	if copyErr != nil {
		return fmt.Errorf("write integration backup: %w", copyErr)
	}
	return closeErr
}

func upsertCodexConfig(existing string, executable string, profile string) string {
	base := strings.TrimRight(removeConfigBlock(existing), "\n")
	block := strings.Join([]string{
		ConfigBlockStart,
		"[mcp_servers.state]",
		"command = " + strconv.Quote(executable),
		"args = [\"mcp\", \"--profile\", " + strconv.Quote(profile) + "]",
		ConfigBlockEnd,
	}, "\n")
	if base == "" {
		return block + "\n"
	}
	return base + "\n\n" + block + "\n"
}

func removeConfigBlock(existing string) string {
	start := strings.Index(existing, ConfigBlockStart)
	if start < 0 {
		return existing
	}
	endOffset := strings.Index(existing[start:], ConfigBlockEnd)
	if endOffset < 0 {
		return existing
	}
	end := start + endOffset + len(ConfigBlockEnd)
	for end < len(existing) && existing[end] == '\n' {
		end++
	}
	before := strings.TrimRight(existing[:start], "\n")
	after := strings.TrimLeft(existing[end:], "\n")
	if before == "" {
		return after
	}
	if after == "" {
		return before + "\n"
	}
	return before + "\n\n" + after
}

func upsertClaudeConfig(existing string, executable string, profile string) (string, error) {
	config, err := decodeJSONObject(existing)
	if err != nil {
		return "", err
	}
	servers := nestedObject(config, "mcpServers")
	servers["state"] = map[string]any{
		"command": executable,
		"args":    []string{"mcp", "--profile", profile},
	}
	return encodeJSONObject(config)
}

func upsertOpenCodeConfig(existing string, executable string, profile string) (string, error) {
	config, err := decodeJSONObject(existing)
	if err != nil {
		return "", err
	}
	servers := nestedObject(config, "mcp")
	servers["state"] = map[string]any{
		"type":    "local",
		"command": []string{executable, "mcp", "--profile", profile},
		"enabled": true,
	}
	return encodeJSONObject(config)
}

func removeJSONIntegration(existing string, containerKey string) (string, error) {
	if strings.TrimSpace(existing) == "" {
		return existing, nil
	}
	config, err := decodeJSONObject(existing)
	if err != nil {
		return "", err
	}
	if container, ok := config[containerKey].(map[string]any); ok {
		delete(container, "state")
	}
	return encodeJSONObject(config)
}

func decodeJSONObject(existing string) (map[string]any, error) {
	if strings.TrimSpace(existing) == "" {
		return make(map[string]any), nil
	}
	config := make(map[string]any)
	if err := json.Unmarshal([]byte(existing), &config); err != nil {
		return nil, fmt.Errorf("decode harness configuration: %w", err)
	}
	return config, nil
}

func nestedObject(config map[string]any, key string) map[string]any {
	if existing, ok := config[key].(map[string]any); ok {
		return existing
	}
	created := make(map[string]any)
	config[key] = created
	return created
}

func encodeJSONObject(config map[string]any) (string, error) {
	encoded, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return "", err
	}
	return string(encoded) + "\n", nil
}

func readOptional(path string) (string, error) {
	contents, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return string(contents), nil
}

// WriteAtomic writes a file through a temporary file plus rename, with the
// given permissions and a 0700 parent directory. Shared with the runner for
// runner.json and the .state/runs projection.
func WriteAtomic(path string, contents []byte, mode os.FileMode) error {
	return writeAtomic(path, contents, mode)
}

func writeAtomic(path string, contents []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".state-integration-*.tmp")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(mode); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(contents); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, path)
}
