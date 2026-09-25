package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/google/uuid"

	"github.com/nicremo/state/internal/state"
)

// runAgentProject sets up an agent on this server as its owner: a project
// (the folder name under the runner's work root), a policy with the agent
// and its rights, and the named runner serving the project. It runs locally
// on the server's data directory, like bootstrap-token, and is idempotent:
// an existing project or policy with the same name is reused.
func runAgentProject(args []string, stdout io.Writer, stderr io.Writer) error {
	flags := flag.NewFlagSet("state-server agent-project", flag.ContinueOnError)
	flags.SetOutput(stderr)
	dataDirectory := flags.String("data", environmentOrDefault("STATE_DATA_DIR", "/data"), "persistent data directory")
	name := flags.String("name", "", "project name, the folder name under the runner's work root, for example karla-report")
	description := flags.String("description", "", "what the project is")
	adapter := flags.String("adapter", "claude-code", "agent: claude-code, codex, kimi-code, opencode, pi-agent or deepseek-harness")
	rights := flags.String("rights", "edit", "what the agent may do without asking: read, edit or full")
	runnerName := flags.String("runner", "", "display name of the runner that serves the project, for example \"MacBook Pro\"")
	timeout := flags.Int("timeout", 60, "minutes one round may take")
	if err := flags.Parse(args); err != nil {
		return err
	}
	capabilities, err := capabilitiesFor(*rights)
	if err != nil {
		return err
	}
	if !state.ValidProjectSlug(*name) {
		return fmt.Errorf("project name %q must be a lowercase slug such as karla-report", *name)
	}
	app, err := newApplication(applicationConfig{dataDirectory: *dataDirectory, version: version})
	if err != nil {
		return err
	}
	defer app.close()
	ctx := context.Background()
	owner, err := app.auth.ExistingOwner(ctx)
	if errors.Is(err, state.ErrNotFound) {
		return errors.New("this server has no owner yet; pair the owner first")
	}
	if err != nil {
		return err
	}

	project, err := findOrCreateProject(ctx, app.state, owner, *name, *description)
	if err != nil {
		return err
	}
	policy, err := findOrCreatePolicy(ctx, app.state, owner, project, *adapter, capabilities, *timeout)
	if err != nil {
		return err
	}
	runnerID := ""
	if strings.TrimSpace(*runnerName) != "" {
		runnerID, err = serveProject(ctx, app.state, owner, *runnerName, project.ID, *adapter)
		if err != nil {
			return err
		}
	}
	return json.NewEncoder(stdout).Encode(map[string]string{"project_id": project.ID, "policy_id": policy.ID, "runner_id": runnerID})
}

// capabilitiesFor maps the three rights levels to the policy vocabulary the
// runner turns into CLI flags.
func capabilitiesFor(rights string) ([]string, error) {
	read := []string{state.CapabilityReadRepository, state.CapabilityReadStateContext}
	switch rights {
	case "read":
		return read, nil
	case "edit":
		return append(read, state.CapabilityEditRepository), nil
	case "full":
		return append(read, state.CapabilityEditRepository, state.CapabilityRunTests, state.CapabilityNetworkAccess), nil
	default:
		return nil, fmt.Errorf("rights must be read, edit or full, not %q", rights)
	}
}

func findOrCreateProject(ctx context.Context, service *state.Service, owner state.Actor, name string, description string) (state.Project, error) {
	projects, err := service.ListProjects(ctx)
	if err != nil {
		return state.Project{}, err
	}
	for _, project := range projects {
		if project.Name == name {
			return project, nil
		}
	}
	return service.CreateProject(ctx, owner, state.CreateProjectInput{Name: name, Description: description, Source: "state-server", ClientRequestID: uuid.NewString()})
}

func findOrCreatePolicy(ctx context.Context, service *state.Service, owner state.Actor, project state.Project, adapter string, capabilities []string, timeout int) (state.ExecutionPolicy, error) {
	name := project.Name + "-" + adapter
	policies, err := service.ListPolicies(ctx)
	if err != nil {
		return state.ExecutionPolicy{}, err
	}
	for _, policy := range policies {
		if policy.Name == name && policy.ProjectID == project.ID {
			return policy, nil
		}
	}
	return service.CreatePolicy(ctx, owner, state.CreatePolicyInput{
		Name:                name,
		ProjectID:           project.ID,
		Adapter:             adapter,
		Mode:                state.ExecutionModeSupervised,
		AllowedCapabilities: capabilities,
		NotifyOnCompletion:  true,
		NotifyOnFailure:     true,
		TimeoutMinutes:      timeout,
		Source:              "state-server",
		ClientRequestID:     uuid.NewString(),
	})
}

// serveProject adds the project and the agent to the runner's server-side
// scopes. The runner's local configuration must list them too.
func serveProject(ctx context.Context, service *state.Service, owner state.Actor, runnerName string, projectID string, adapter string) (string, error) {
	runners, err := service.ListRunners(ctx)
	if err != nil {
		return "", err
	}
	for _, runner := range runners {
		if runner.DisplayName != runnerName {
			continue
		}
		projects, adapters := appendMissing(runner.Projects, projectID), appendMissing(runner.Adapters, adapter)
		if len(projects) == len(runner.Projects) && len(adapters) == len(runner.Adapters) {
			return runner.ID, nil
		}
		if _, err := service.UpdateRunner(ctx, owner, runner.ID, state.UpdateRunnerInput{Projects: &projects, Adapters: &adapters, ExpectedRevision: runner.Revision, Source: "state-server", ClientRequestID: uuid.NewString()}); err != nil {
			return "", err
		}
		return runner.ID, nil
	}
	return "", fmt.Errorf("no runner named %q is paired with this server", runnerName)
}

func appendMissing(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(append([]string(nil), values...), value)
}
