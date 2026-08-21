package statectl

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/nicremo/state/internal/state"
)

// PolicyFileName is the local policy template inside a project's .state
// directory.
const PolicyFileName = "policy.yaml"

// DefaultPolicyTemplate is the commented policy template written by
// statectl project init. It covers every ExecutionPolicy field and validates
// cleanly as shipped.
const DefaultPolicyTemplate = `# State execution policy (local template).
#
# This file is a local, reviewable description of what an agent run may do in
# this repository. The canonical policy lives on the State server and the
# server validates every run against it; editing this file never changes
# server-side permissions by itself.
#
# Validate with: statectl project validate

# Policy name, a slug: ^[a-z0-9][a-z0-9-]{0,62}[a-z0-9]$
name: nightly-review

# Server project ID this policy belongs to (see .state/project.json). Leave
# empty until the owner creates the policy on the server.
project_id: ""

# Harness adapter that executes runs, for example codex, claude-code or
# opencode. Any valid harness slug paired through statectl works.
adapter: codex

# supervised | unattended-low-risk
# unattended-low-risk may only carry read_repository, read_state_context,
# run_tests and edit_repository capabilities.
mode: supervised

# Closed capability vocabulary:
#   read_repository, edit_repository, run_tests, read_state_context,
#   write_state, network_access, deploy, message_external, destructive
allowed_capabilities:
  - read_repository
  - run_tests

# When true, a verified successful run completes the originating State
# occurrence automatically.
mark_occurrence_done_on_success: true

# Push notifications to paired devices.
notify_on_start: false
notify_on_completion: true
notify_on_failure: true

# Run timeout in minutes (1..240). The runner lease is twice this value.
timeout_minutes: 30

# A disabled policy never materializes runs.
enabled: false
`

// policyFile mirrors state.ExecutionPolicy for YAML decoding. It stays local
// so the domain type keeps its single JSON projection.
type policyFile struct {
	Name                        string   `yaml:"name"`
	ProjectID                   string   `yaml:"project_id"`
	Adapter                     string   `yaml:"adapter"`
	Mode                        string   `yaml:"mode"`
	AllowedCapabilities         []string `yaml:"allowed_capabilities"`
	MarkOccurrenceDoneOnSuccess bool     `yaml:"mark_occurrence_done_on_success"`
	NotifyOnStart               bool     `yaml:"notify_on_start"`
	NotifyOnCompletion          bool     `yaml:"notify_on_completion"`
	NotifyOnFailure             bool     `yaml:"notify_on_failure"`
	TimeoutMinutes              int      `yaml:"timeout_minutes"`
	Enabled                     bool     `yaml:"enabled"`
}

// PolicyValidation is the outcome of checking one .state/policy.yaml file.
// Findings lists every problem in stable order; an empty slice means valid.
type PolicyValidation struct {
	Policy   state.ExecutionPolicy
	Findings []string
}

// ValidatePolicy parses and validates the policy file contents. The projectID
// of the surrounding .state/project.json (empty when unknown) is cross-checked
// when the policy names one.
func ValidatePolicy(contents []byte, projectID string) (PolicyValidation, error) {
	decoder := yaml.NewDecoder(bytes.NewReader(contents))
	decoder.KnownFields(true)
	var parsed policyFile
	if err := decoder.Decode(&parsed); err != nil {
		return PolicyValidation{}, fmt.Errorf("parse policy.yaml: %w", err)
	}
	policy := state.ExecutionPolicy{
		Name:                        strings.TrimSpace(parsed.Name),
		ProjectID:                   strings.TrimSpace(parsed.ProjectID),
		Adapter:                     strings.TrimSpace(parsed.Adapter),
		Mode:                        state.ExecutionMode(strings.TrimSpace(parsed.Mode)),
		AllowedCapabilities:         parsed.AllowedCapabilities,
		MarkOccurrenceDoneOnSuccess: parsed.MarkOccurrenceDoneOnSuccess,
		NotifyOnStart:               parsed.NotifyOnStart,
		NotifyOnCompletion:          parsed.NotifyOnCompletion,
		NotifyOnFailure:             parsed.NotifyOnFailure,
		TimeoutMinutes:              parsed.TimeoutMinutes,
		Enabled:                     parsed.Enabled,
	}
	validation := PolicyValidation{Policy: policy}
	add := func(format string, args ...any) {
		validation.Findings = append(validation.Findings, fmt.Sprintf(format, args...))
	}

	if !state.ValidProjectSlug(policy.Name) {
		add("name %q is not a valid slug (^[a-z0-9][a-z0-9-]{0,62}[a-z0-9]$)", policy.Name)
	}
	if !state.ValidHarness(policy.Adapter) {
		add("adapter %q is not a valid harness slug", policy.Adapter)
	}
	if policy.Mode != state.ExecutionModeSupervised && policy.Mode != state.ExecutionModeUnattendedLowRisk {
		add("mode %q must be %q or %q", policy.Mode, state.ExecutionModeSupervised, state.ExecutionModeUnattendedLowRisk)
	}
	if policy.TimeoutMinutes < state.PolicyTimeoutMinMinutes || policy.TimeoutMinutes > state.PolicyTimeoutMaxMinutes {
		add("timeout_minutes %d is outside %d..%d", policy.TimeoutMinutes, state.PolicyTimeoutMinMinutes, state.PolicyTimeoutMaxMinutes)
	}
	seen := make(map[string]struct{}, len(policy.AllowedCapabilities))
	for _, capability := range policy.AllowedCapabilities {
		if !state.ValidCapability(capability) {
			add("allowed_capabilities entry %q is not in the capability vocabulary", capability)
			continue
		}
		if _, duplicate := seen[capability]; duplicate {
			add("allowed_capabilities entry %q is duplicated", capability)
			continue
		}
		seen[capability] = struct{}{}
		if policy.Mode == state.ExecutionModeUnattendedLowRisk && !state.ValidUnattendedCapability(capability) {
			add("capability %q is not allowed in %q mode", capability, state.ExecutionModeUnattendedLowRisk)
		}
	}
	if projectID != "" && policy.ProjectID != "" && policy.ProjectID != projectID {
		add("project_id %q does not match .state/project.json (%q)", policy.ProjectID, projectID)
	}

	// The domain validator is the final authority; it must agree with the
	// granular checks above.
	if len(validation.Findings) == 0 {
		if err := state.ValidPolicyConfiguration(policy); err != nil {
			add("server-side validation rejects this policy: %v", err)
		}
	}
	sort.Strings(validation.Findings)
	return validation, nil
}

// ValidatePolicyDir validates <dir>/.state/policy.yaml, cross-checking the
// project identity when .state/project.json exists.
func ValidatePolicyDir(dir string) (PolicyValidation, error) {
	policyPath := filepath.Join(dir, stateDirName, PolicyFileName)
	contents, err := os.ReadFile(policyPath)
	if errors.Is(err, os.ErrNotExist) {
		return PolicyValidation{}, fmt.Errorf("no %s found — run statectl project init first", filepath.Join(stateDirName, PolicyFileName))
	}
	if err != nil {
		return PolicyValidation{}, fmt.Errorf("read %s: %w", policyPath, err)
	}
	projectID := ""
	if project, err := ReadProjectFile(dir); err == nil {
		projectID = project.ProjectID
	}
	return ValidatePolicy(contents, projectID)
}
