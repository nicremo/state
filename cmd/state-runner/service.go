package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/nicremo/state/internal/runner"
)

// servicePlatformError explains why the command is macOS only.
const servicePlatformError = "service management is only implemented for macOS; use systemd on Linux (see docs/runner-service.md)"

// defaultAgentPathEntries are prepended to the user's PATH so the launch agent
// finds the agent CLIs an interactive shell finds.
var defaultAgentPathEntries = []string{".local/bin", "/opt/homebrew/bin", "/usr/local/bin"}

func runService(args []string, stdout io.Writer, stderr io.Writer) error {
	if err := checkServicePlatform(runtime.GOOS); err != nil {
		return err
	}
	if len(args) == 0 {
		return errors.New("usage: state-runner service <install|uninstall|status>")
	}
	switch args[0] {
	case "install":
		return runServiceInstall(args[1:], stdout, stderr)
	case "uninstall":
		return runServiceUninstall(args[1:], stdout, stderr)
	case "status":
		return runServiceStatus(args[1:], stdout, stderr)
	default:
		return fmt.Errorf("unknown service command %q", args[0])
	}
}

// checkServicePlatform gates the command to macOS without hiding the Linux path.
func checkServicePlatform(goos string) error {
	if goos != "darwin" {
		return errors.New(servicePlatformError)
	}
	return nil
}

func runServiceInstall(args []string, stdout io.Writer, stderr io.Writer) error {
	flags := flag.NewFlagSet("state-runner service install", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configPath := flags.String("config", defaultConfigPath(), "state-runner config path")
	agentPath := flags.String("path", "", "PATH for the launch agent (default: this PATH plus ~/.local/bin, /opt/homebrew/bin, /usr/local/bin)")
	if err := flags.Parse(args); err != nil {
		return err
	}
	absoluteConfig, err := filepath.Abs(*configPath)
	if err != nil {
		return fmt.Errorf("resolve config path: %w", err)
	}
	if err := ensureRunnerPaired(absoluteConfig); err != nil {
		return err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("resolve home directory: %w", err)
	}
	executable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve state-runner executable: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(executable)
	if err != nil {
		return fmt.Errorf("resolve state-runner executable: %w", err)
	}
	path := *agentPath
	if path == "" {
		path = defaultAgentPath(os.Getenv("PATH"), home)
	}
	spec := runner.ServiceSpec{
		Executable: resolved,
		ConfigPath: absoluteConfig,
		LogDir:     runner.ServiceLogDir(home),
		Path:       path,
		Home:       home,
	}
	manager := runner.NewServiceManager(runner.LaunchAgentsDir(home), runner.NewExecLaunchctl(), os.Getuid())
	plistPath, err := manager.Install(spec)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(
		stdout,
		"installed launch agent %s\nrunner logs: %s\ncheck with: state-runner service status\n",
		plistPath,
		spec.LogDir,
	)
	return err
}

func runServiceUninstall(args []string, stdout io.Writer, stderr io.Writer) error {
	flags := flag.NewFlagSet("state-runner service uninstall", flag.ContinueOnError)
	flags.SetOutput(stderr)
	if err := flags.Parse(args); err != nil {
		return err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("resolve home directory: %w", err)
	}
	manager := runner.NewServiceManager(runner.LaunchAgentsDir(home), runner.NewExecLaunchctl(), os.Getuid())
	if err := manager.Uninstall(); err != nil {
		return err
	}
	_, err = fmt.Fprintf(stdout, "removed launch agent %s\n", manager.PlistPath())
	return err
}

func runServiceStatus(args []string, stdout io.Writer, stderr io.Writer) error {
	flags := flag.NewFlagSet("state-runner service status", flag.ContinueOnError)
	flags.SetOutput(stderr)
	if err := flags.Parse(args); err != nil {
		return err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("resolve home directory: %w", err)
	}
	manager := runner.NewServiceManager(runner.LaunchAgentsDir(home), runner.NewExecLaunchctl(), os.Getuid())
	running, detail, err := manager.Status()
	if err != nil {
		return fmt.Errorf("launch agent %s is not loaded: %w", runner.ServiceLabel, err)
	}
	if running {
		_, err = fmt.Fprintf(stdout, "state-runner is running (%s)\n", runner.ServiceLabel)
		return err
	}
	_, err = fmt.Fprintf(stdout, "state-runner is not running (%s reports %s)\n", runner.ServiceLabel, firstStateLine(detail))
	return err
}

// ensureRunnerPaired refuses to install an agent that has no credential yet.
func ensureRunnerPaired(configPath string) error {
	if _, err := os.Stat(configPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return errors.New("runner is not paired yet, run state-runner pair first")
		}
		return fmt.Errorf("inspect runner config %s: %w", configPath, err)
	}
	return nil
}

// defaultAgentPath merges the shell PATH with the directories local agent
// tools install into, without duplicates.
func defaultAgentPath(shellPath string, home string) string {
	entries := make([]string, 0, 8)
	seen := make(map[string]bool)
	add := func(entry string) {
		if entry == "" || seen[entry] {
			return
		}
		seen[entry] = true
		entries = append(entries, entry)
	}
	for _, entry := range strings.Split(shellPath, ":") {
		add(strings.TrimSpace(entry))
	}
	for _, entry := range defaultAgentPathEntries {
		if strings.HasPrefix(entry, "/") {
			add(entry)
			continue
		}
		add(filepath.Join(home, entry))
	}
	return strings.Join(entries, ":")
}

// firstStateLine picks the launchctl state line for a short status message.
func firstStateLine(detail string) string {
	for _, line := range strings.Split(detail, "\n") {
		if trimmed := strings.TrimSpace(line); strings.HasPrefix(trimmed, "state =") {
			return trimmed
		}
	}
	return "no state"
}
