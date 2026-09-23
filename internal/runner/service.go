package runner

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"path/filepath"
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
