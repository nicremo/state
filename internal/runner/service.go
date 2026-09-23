package runner

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// ServiceLabel is the launchd label of the per-user state-runner agent.
const ServiceLabel = "com.fabincrm.state.runner"

// ServiceSpec is everything the launch agent needs to keep one runner alive.
type ServiceSpec struct {
	Executable string // absolute path of state-runner
	ConfigPath string // absolute path of runner.json
	LogDir     string // absolute directory for stdout/stderr logs
	Path       string // PATH for the agent, so adapters find claude, codex, opencode, pi
	Home       string
}

// LaunchAgentPlist renders the property list for a per-user LaunchAgent that
// keeps `state-runner run --config <ConfigPath>` alive.
func LaunchAgentPlist(spec ServiceSpec) ([]byte, error) {
	if err := spec.validate(); err != nil {
		return nil, err
	}
	var buffer bytes.Buffer
	buffer.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	buffer.WriteString(`<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">` + "\n")
	buffer.WriteString(`<plist version="1.0">` + "\n")
	buffer.WriteString("<dict>\n")
	writeKey(&buffer, "Label")
	writeString(&buffer, ServiceLabel)
	writeKey(&buffer, "ProgramArguments")
	buffer.WriteString("\t<array>\n")
	for _, argument := range []string{spec.Executable, "run", "--config", spec.ConfigPath} {
		writeString(&buffer, argument)
	}
	buffer.WriteString("\t</array>\n")
	writeKey(&buffer, "RunAtLoad")
	buffer.WriteString("\t<true/>\n")
	writeKey(&buffer, "KeepAlive")
	buffer.WriteString("\t<true/>\n")
	writeKey(&buffer, "ProcessType")
	writeString(&buffer, "Background")
	writeKey(&buffer, "ThrottleInterval")
	buffer.WriteString("\t<integer>30</integer>\n")
	writeKey(&buffer, "StandardOutPath")
	writeString(&buffer, filepath.Join(spec.LogDir, "runner.out.log"))
	writeKey(&buffer, "StandardErrorPath")
	writeString(&buffer, filepath.Join(spec.LogDir, "runner.err.log"))
	writeKey(&buffer, "EnvironmentVariables")
	buffer.WriteString("\t<dict>\n")
	writeKey(&buffer, "PATH")
	writeString(&buffer, spec.Path)
	writeKey(&buffer, "HOME")
	writeString(&buffer, spec.Home)
	buffer.WriteString("\t</dict>\n")
	buffer.WriteString("</dict>\n")
	buffer.WriteString("</plist>\n")
	return buffer.Bytes(), nil
}

// validate rejects specs that launchd would resolve against the wrong
// directory or that would leave the runner without a usable PATH.
func (spec ServiceSpec) validate() error {
	for _, field := range []struct {
		name  string
		value string
	}{
		{name: "executable", value: spec.Executable},
		{name: "config path", value: spec.ConfigPath},
		{name: "log directory", value: spec.LogDir},
		{name: "home directory", value: spec.Home},
	} {
		if field.value == "" {
			return fmt.Errorf("launch agent %s is empty", field.name)
		}
		if !filepath.IsAbs(field.value) {
			return fmt.Errorf("launch agent %s must be absolute: %q", field.name, field.value)
		}
	}
	if spec.Path == "" {
		return errors.New("launch agent PATH is empty")
	}
	return nil
}

func writeKey(buffer *bytes.Buffer, key string) {
	buffer.WriteString("\t<key>")
	_ = xml.EscapeText(buffer, []byte(key))
	buffer.WriteString("</key>\n")
}

func writeString(buffer *bytes.Buffer, value string) {
	buffer.WriteString("\t<string>")
	_ = xml.EscapeText(buffer, []byte(value))
	buffer.WriteString("</string>\n")
}

// LaunchAgentsDir is the per-user launch agent directory of the given home.
func LaunchAgentsDir(home string) string {
	return filepath.Join(home, "Library", "LaunchAgents")
}

// ServiceLogDir is the directory that receives the runner stdout/stderr logs.
func ServiceLogDir(home string) string {
	return filepath.Join(home, "Library", "Logs", "State Runner")
}

// Launchctl abstracts the launchctl calls so tests never touch the real system.
type Launchctl interface {
	Bootstrap(domain string, plistPath string) error   // launchctl bootstrap gui/<uid> <plist>
	Bootout(domain string, label string) error         // launchctl bootout gui/<uid>/<label>
	Print(domain string, label string) (string, error) // launchctl print gui/<uid>/<label>
}

// ServiceManager installs, removes and inspects the per-user runner agent.
type ServiceManager struct {
	launchAgentsDir string
	launchctl       Launchctl
	uid             int
}

// NewServiceManager returns a manager that writes into launchAgentsDir and
// talks to launchctl through the given implementation.
func NewServiceManager(launchAgentsDir string, launchctl Launchctl, uid int) *ServiceManager {
	return &ServiceManager{launchAgentsDir: launchAgentsDir, launchctl: launchctl, uid: uid}
}

// Domain is the launchd domain of the logged-in user, for example gui/501.
func (manager *ServiceManager) Domain() string {
	return fmt.Sprintf("gui/%d", manager.uid)
}

// PlistPath is the launch agent file this manager owns.
func (manager *ServiceManager) PlistPath() string {
	return filepath.Join(manager.launchAgentsDir, ServiceLabel+".plist")
}

// Install writes the launch agent and loads it. It is idempotent: an already
// loaded agent is booted out first, and a missing one is fine.
func (manager *ServiceManager) Install(spec ServiceSpec) (plistPath string, err error) {
	contents, err := LaunchAgentPlist(spec)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(manager.launchAgentsDir, 0o755); err != nil {
		return "", fmt.Errorf("create launch agents directory: %w", err)
	}
	if err := os.MkdirAll(spec.LogDir, 0o755); err != nil {
		return "", fmt.Errorf("create runner log directory: %w", err)
	}
	plistPath = manager.PlistPath()
	if err := os.WriteFile(plistPath, contents, 0o644); err != nil {
		return "", fmt.Errorf("write launch agent: %w", err)
	}
	// A bootout failure only means the agent is not loaded yet.
	_ = manager.launchctl.Bootout(manager.Domain(), ServiceLabel)
	if err := manager.launchctl.Bootstrap(manager.Domain(), plistPath); err != nil {
		return "", fmt.Errorf("load launch agent %s: %w", plistPath, err)
	}
	return plistPath, nil
}

// Uninstall boots the agent out and removes its plist. Both steps tolerate an
// agent that was never installed.
func (manager *ServiceManager) Uninstall() error {
	_ = manager.launchctl.Bootout(manager.Domain(), ServiceLabel)
	if err := os.Remove(manager.PlistPath()); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove launch agent: %w", err)
	}
	return nil
}

// Status reports whether launchd currently runs the agent.
func (manager *ServiceManager) Status() (running bool, detail string, err error) {
	output, err := manager.launchctl.Print(manager.Domain(), ServiceLabel)
	if err != nil {
		return false, output, err
	}
	return strings.Contains(output, "state = running"), output, nil
}

// ExecLaunchctl talks to the real /bin/launchctl with fixed arguments.
type ExecLaunchctl struct{}

// NewExecLaunchctl returns the production launchctl implementation.
func NewExecLaunchctl() Launchctl {
	return ExecLaunchctl{}
}

func (ExecLaunchctl) Bootstrap(domain string, plistPath string) error {
	return runLaunchctl("bootstrap", domain, plistPath)
}

func (ExecLaunchctl) Bootout(domain string, label string) error {
	return runLaunchctl("bootout", domain+"/"+label)
}

func (ExecLaunchctl) Print(domain string, label string) (string, error) {
	command := exec.Command("/bin/launchctl", "print", domain+"/"+label)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		return stdout.String(), fmt.Errorf("launchctl print %s/%s: %w: %s", domain, label, err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}

func runLaunchctl(arguments ...string) error {
	command := exec.Command("/bin/launchctl", arguments...)
	var stderr bytes.Buffer
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		return fmt.Errorf("launchctl %s: %w: %s", strings.Join(arguments, " "), err, strings.TrimSpace(stderr.String()))
	}
	return nil
}
