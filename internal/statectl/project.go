package statectl

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/nicremo/state/internal/state"
)

// stateDirName is the repository-local projection directory of a project.
const stateDirName = ".state"

// projectFileName pins the server-side project identity of a checkout.
const projectFileName = "project.json"

// ErrProjectMismatch reports an existing .state/project.json that pins a
// different server project than the one being initialized.
var ErrProjectMismatch = errors.New(".state/project.json pins a different project")

// ProjectFile is the schema of .state/project.json.
type ProjectFile struct {
	SchemaVersion int    `json:"schema_version"`
	ProjectID     string `json:"project_id"`
	ProjectName   string `json:"project_name"`
	Server        string `json:"server"`
}

// ProjectInitRequest parametrizes `statectl project init`.
type ProjectInitRequest struct {
	Name        string
	RootPath    string
	ProfileName string
	Dir         string
}

// ProjectInitResult reports what init did, for CLI output and tests.
type ProjectInitResult struct {
	Project state.Project
	Created bool
	Written []string
	Kept    []string
}

// ProjectSyncRequest parametrizes `statectl project sync`.
type ProjectSyncRequest struct {
	ProfileName   string
	Dir           string
	ClientVersion string
}

// ProjectService resolves server projects and maintains the local .state
// projection of a checkout.
type ProjectService struct {
	config     *ConfigStore
	secrets    SecretStore
	httpClient *http.Client
}

func NewProjectService(config *ConfigStore, secrets SecretStore, httpClient *http.Client) *ProjectService {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 20 * time.Second}
	}
	return &ProjectService{config: config, secrets: secrets, httpClient: httpClient}
}

// Init resolves or creates the server project and writes the .state
// projection. Re-runs are idempotent and never overwrite a project.json
// pinning a different project.
func (service *ProjectService) Init(ctx context.Context, request ProjectInitRequest) (ProjectInitResult, error) {
	name := strings.TrimSpace(request.Name)
	if service.config == nil || service.secrets == nil || !state.ValidProjectSlug(name) || request.ProfileName == "" || request.Dir == "" {
		return ProjectInitResult{}, state.ErrInvalidInput
	}
	profile, token, err := service.profileAndToken(request.ProfileName)
	if err != nil {
		return ProjectInitResult{}, err
	}

	project, created, err := service.resolveProject(ctx, profile, token, name, strings.TrimSpace(request.RootPath))
	if err != nil {
		return ProjectInitResult{}, err
	}

	result := ProjectInitResult{Project: project, Created: created}
	projectFile := ProjectFile{
		SchemaVersion: 1,
		ProjectID:     project.ID,
		ProjectName:   project.Name,
		Server:        profile.ServerURL,
	}
	if existing, err := ReadProjectFile(request.Dir); err == nil && existing.ProjectID != project.ID {
		return ProjectInitResult{}, fmt.Errorf("%w: %s points at %s (%q), refusing to overwrite with %q — remove .state/project.json only if this checkout really changes projects",
			ErrProjectMismatch, filepath.Join(request.Dir, stateDirName, projectFileName), existing.ProjectID, existing.ProjectName, project.Name)
	}

	writes := []struct {
		relative string
		contents []byte
	}{
		{filepath.Join(stateDirName, "README.md"), []byte(projectReadme(projectFile))},
		{filepath.Join(stateDirName, projectFileName), mustMarshalProjectFile(projectFile)},
		{filepath.Join(stateDirName, PolicyFileName), []byte(DefaultPolicyTemplate)},
		{filepath.Join(stateDirName, ".gitignore"), []byte(stateGitignore)},
	}
	for _, write := range writes {
		path := filepath.Join(request.Dir, write.relative)
		if _, err := os.Stat(path); err == nil {
			result.Kept = append(result.Kept, write.relative)
			continue
		} else if !errors.Is(err, os.ErrNotExist) {
			return ProjectInitResult{}, err
		}
		if err := writeAtomic(path, write.contents, 0o600); err != nil {
			return ProjectInitResult{}, err
		}
		result.Written = append(result.Written, write.relative)
	}
	sort.Strings(result.Written)
	sort.Strings(result.Kept)
	return result, nil
}

// resolveProject finds the named project or, when the credential is allowed
// to, creates it. Project creation is owner-gated server-side, so harness and
// device tokens receive a 403 with guidance.
func (service *ProjectService) resolveProject(ctx context.Context, profile Profile, token string, name string, rootPathHint string) (state.Project, bool, error) {
	projects, err := service.listProjects(ctx, profile, token)
	if err != nil {
		return state.Project{}, false, err
	}
	for _, candidate := range projects {
		if candidate.Name == name {
			return candidate, false, nil
		}
	}
	input := state.CreateProjectInput{
		Name:            name,
		RootPathHint:    rootPathHint,
		ClientRequestID: uuid.NewString(),
		Source:          "statectl",
	}
	project, err := service.createProject(ctx, profile, token, input)
	if err == nil {
		return project, true, nil
	}
	var statusErr *serverStatusError
	if errors.As(err, &statusErr) && statusErr.status == http.StatusForbidden {
		return state.Project{}, false, fmt.Errorf("the server refused to create project %q (owner-only operation): create it in the iOS app or pair an owner credential, then re-run statectl project init", name)
	}
	return state.Project{}, false, err
}

// Sync refreshes .state/context/current.md from the server briefing through
// the profile's MCP session. The briefing is bounded and redacted
// server-side; sync never writes credential material.
func (service *ProjectService) Sync(ctx context.Context, request ProjectSyncRequest) (string, error) {
	if service.config == nil || service.secrets == nil || request.ProfileName == "" || request.Dir == "" {
		return "", state.ErrInvalidInput
	}
	project, err := ReadProjectFile(request.Dir)
	if err != nil {
		return "", err
	}
	profile, token, err := service.profileAndToken(request.ProfileName)
	if err != nil {
		return "", err
	}
	session, err := ConnectRemote(ctx, profile, token, request.ClientVersion)
	if err != nil {
		return "", err
	}
	defer session.Close()
	callResult, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "get_briefing",
		Arguments: map[string]any{"limit": 50},
	})
	if err != nil {
		return "", fmt.Errorf("fetch briefing: %w", err)
	}
	if callResult.IsError {
		return "", fmt.Errorf("fetch briefing: server rejected get_briefing")
	}
	encoded, err := json.Marshal(callResult.StructuredContent)
	if err != nil {
		return "", fmt.Errorf("encode briefing: %w", err)
	}
	var briefing state.Briefing
	if err := json.Unmarshal(encoded, &briefing); err != nil {
		return "", fmt.Errorf("decode briefing: %w", err)
	}
	path := filepath.Join(request.Dir, stateDirName, "context", "current.md")
	if err := writeAtomic(path, []byte(RenderBriefingMarkdown(project, briefing)), 0o600); err != nil {
		return "", err
	}
	return path, nil
}

// Validate checks <dir>/.state/policy.yaml offline against the execution
// policy rules.
func (service *ProjectService) Validate(dir string) (PolicyValidation, error) {
	if dir == "" {
		return PolicyValidation{}, state.ErrInvalidInput
	}
	return ValidatePolicyDir(dir)
}

// ReadProjectFile reads <dir>/.state/project.json.
func ReadProjectFile(dir string) (ProjectFile, error) {
	path := filepath.Join(dir, stateDirName, projectFileName)
	contents, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return ProjectFile{}, fmt.Errorf("no %s found in %s — run statectl project init first", filepath.Join(stateDirName, projectFileName), dir)
	}
	if err != nil {
		return ProjectFile{}, err
	}
	var project ProjectFile
	if err := json.Unmarshal(contents, &project); err != nil {
		return ProjectFile{}, fmt.Errorf("decode %s: %w", path, err)
	}
	if project.SchemaVersion != 1 || project.ProjectID == "" || project.ProjectName == "" {
		return ProjectFile{}, fmt.Errorf("%s is incomplete or has an unsupported schema version", path)
	}
	return project, nil
}

// RenderBriefingMarkdown renders the bounded project context file.
func RenderBriefingMarkdown(project ProjectFile, briefing state.Briefing) string {
	var builder strings.Builder
	fmt.Fprintf(&builder, "# State context — %s\n\n", project.ProjectName)
	fmt.Fprintf(&builder, "Generated %s by statectl project sync. Cursor: %d.\n", briefing.GeneratedAt.UTC().Format(time.RFC3339), briefing.Cursor)
	fmt.Fprintf(&builder, "Bounded and redacted server-side. Refresh with `statectl project sync`. Do not edit by hand; nothing here is a credential.\n")
	if summary := strings.TrimSpace(briefing.Summary); summary != "" {
		fmt.Fprintf(&builder, "\n## Summary\n\n%s\n", summary)
	}
	if len(briefing.Reminders) > 0 {
		builder.WriteString("\n## Reminders\n\n")
		for _, reminder := range briefing.Reminders {
			fmt.Fprintf(&builder, "- %s (id %s, status %s", reminder.Title, reminder.ID, reminder.Status)
			if reminder.Schedule != nil && reminder.Schedule.LocalDate != "" {
				fmt.Fprintf(&builder, ", scheduled %s", reminder.Schedule.LocalDate)
				if reminder.Schedule.LocalTime != "" {
					fmt.Fprintf(&builder, " %s", reminder.Schedule.LocalTime)
				}
			}
			builder.WriteString(")\n")
			if description := strings.TrimSpace(reminder.Description); description != "" {
				fmt.Fprintf(&builder, "  %s\n", description)
			}
		}
	}
	if len(briefing.Changes) > 0 {
		builder.WriteString("\n## Recent changes\n\n")
		for _, change := range briefing.Changes {
			reference := change.Event.ReminderID
			if reference == "" {
				reference = "-"
			}
			fmt.Fprintf(&builder, "- [%d] %s on %s\n", change.Cursor, change.Event.Action, reference)
		}
	}
	return builder.String()
}

func (service *ProjectService) profileAndToken(profileName string) (Profile, string, error) {
	profile, err := service.config.LoadProfile(profileName)
	if err != nil {
		return Profile{}, "", err
	}
	token, err := service.secrets.Get(profile.CredentialAccount())
	if err != nil {
		return Profile{}, "", err
	}
	return profile, token, nil
}

func (service *ProjectService) listProjects(ctx context.Context, profile Profile, token string) ([]state.Project, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, profile.ServerURL+"/api/v1/projects", nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	response, err := service.httpClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("list projects: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, serverStatusErr("list projects", response)
	}
	var body struct {
		Projects []state.Project `json:"projects"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&body); err != nil {
		return nil, fmt.Errorf("decode project list: %w", err)
	}
	return body.Projects, nil
}

func (service *ProjectService) createProject(ctx context.Context, profile Profile, token string, input state.CreateProjectInput) (state.Project, error) {
	encoded, err := json.Marshal(input)
	if err != nil {
		return state.Project{}, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, profile.ServerURL+"/api/v1/projects", bytes.NewReader(encoded))
	if err != nil {
		return state.Project{}, err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	response, err := service.httpClient.Do(request)
	if err != nil {
		return state.Project{}, fmt.Errorf("create project: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		return state.Project{}, serverStatusErr("create project", response)
	}
	var project state.Project
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&project); err != nil {
		return state.Project{}, fmt.Errorf("decode created project: %w", err)
	}
	return project, nil
}

// serverStatusError carries an unexpected HTTP status plus the server error
// code so callers can special-case answers such as the owner-gated 403.
type serverStatusError struct {
	action string
	status int
	code   string
}

func (err *serverStatusError) Error() string {
	return fmt.Sprintf("%s failed with status %d and code %s", err.action, err.status, err.code)
}

func serverStatusErr(action string, response *http.Response) error {
	var responseError struct {
		Code string `json:"code"`
	}
	_ = json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&responseError)
	if responseError.Code == "" {
		responseError.Code = "request_failed"
	}
	return &serverStatusError{action: action, status: response.StatusCode, code: responseError.Code}
}

func mustMarshalProjectFile(project ProjectFile) []byte {
	encoded, err := json.MarshalIndent(project, "", "  ")
	if err != nil {
		panic(err)
	}
	return append(encoded, '\n')
}

func projectReadme(project ProjectFile) string {
	return fmt.Sprintf(`# .state/

Generated by statectl project init. This directory connects the repository to
the State server at %s as project %q (%s).

- project.json pins the server-side project identity. Do not edit by hand.
- policy.yaml is a local, reviewable template of the execution policy. The
  canonical policy lives on the server; the owner edits it there (iOS app or
  REST) and statectl project validate checks this copy offline.
- context/current.md is a bounded, redacted briefing refreshed by
  statectl project sync and by state-runner before each launch.
- runs/<run-id>/ holds the immutable task contract and the local status of
  each agent run executed in this checkout.

Safety boundary: this directory is never a credential store and grants no
permissions by itself. Secrets do not belong here; .gitignore excludes
context/, runs/, logs and locks. No file from this directory is uploaded, and
a run executes only through a paired state-runner with a hash-pinned contract.
`, project.Server, project.ProjectName, project.ProjectID)
}

const stateGitignore = `context/
runs/
*.log
*.lock
local.yaml
`
