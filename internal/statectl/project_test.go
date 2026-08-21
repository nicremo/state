package statectl

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/pocketbase/pocketbase"

	stateauth "github.com/nicremo/state/internal/auth"
	"github.com/nicremo/state/internal/mcpserver"
	"github.com/nicremo/state/internal/state"
	"github.com/nicremo/state/internal/store"
)

var testProject = state.Project{
	ID:        "01989f4a-ddfa-73a5-a131-3a6ef6a09cba",
	Name:      "customer-api",
	Revision:  1,
	CreatedAt: time.Date(2026, time.August, 21, 12, 0, 0, 0, time.UTC),
	UpdatedAt: time.Date(2026, time.August, 21, 12, 0, 0, 0, time.UTC),
}

// newProjectFixture builds a ProjectService whose profile points at a test
// server, with the credential in a memory keychain.
func newProjectFixture(t *testing.T, server *httptest.Server) (*ProjectService, string) {
	t.Helper()
	configStore := NewConfigStore(filepath.Join(t.TempDir(), "statectl.json"))
	profile := Profile{Name: "codex", ServerURL: server.URL, ActorID: "01989f4a-ddfa-769f-bd09-53052672c44f", Harness: "codex"}
	if err := configStore.SaveProfile(profile); err != nil {
		t.Fatalf("SaveProfile() error = %v", err)
	}
	secrets := &memorySecretStore{values: map[string]string{profile.CredentialAccount(): "state_test_credential"}}
	return NewProjectService(configStore, secrets, server.Client()), profile.Name
}

func TestProjectInitWritesStateDirIdempotently(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/api/v1/projects" || request.Method != http.MethodGet {
			http.NotFound(writer, request)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(map[string]any{"projects": []state.Project{testProject}})
	}))
	t.Cleanup(server.Close)

	service, profileName := newProjectFixture(t, server)
	directory := t.TempDir()
	request := ProjectInitRequest{Name: "customer-api", RootPath: "~/src/customer-api", ProfileName: profileName, Dir: directory}

	result, err := service.Init(context.Background(), request)
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	if result.Created || result.Project.ID != testProject.ID {
		t.Fatalf("Init() result = %#v", result)
	}
	if len(result.Written) != 4 {
		t.Fatalf("Init() wrote %v, want 4 files", result.Written)
	}

	project, err := ReadProjectFile(directory)
	if err != nil {
		t.Fatalf("ReadProjectFile() error = %v", err)
	}
	if project.ProjectID != testProject.ID || project.ProjectName != "customer-api" || project.Server != server.URL || project.SchemaVersion != 1 {
		t.Fatalf("project.json = %#v", project)
	}
	gitignore, err := os.ReadFile(filepath.Join(directory, ".state", ".gitignore"))
	if err != nil {
		t.Fatalf("read .gitignore error = %v", err)
	}
	for _, ignored := range []string{"context/", "runs/", "*.log", "*.lock", "local.yaml"} {
		if !strings.Contains(string(gitignore), ignored) {
			t.Fatalf(".gitignore misses %q:\n%s", ignored, gitignore)
		}
	}
	readme, err := os.ReadFile(filepath.Join(directory, ".state", "README.md"))
	if err != nil || !strings.Contains(string(readme), "Safety boundary") {
		t.Fatalf("README.md = %v, %v", readme, err)
	}
	// The generated policy template validates cleanly as shipped.
	validation, err := service.Validate(directory)
	if err != nil || len(validation.Findings) != 0 {
		t.Fatalf("Validate(template) = %#v, %v", validation.Findings, err)
	}
	info, err := os.Stat(filepath.Join(directory, ".state", "project.json"))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("project.json mode = %v, %v", info.Mode(), err)
	}

	// A second run keeps every existing file.
	second, err := service.Init(context.Background(), request)
	if err != nil {
		t.Fatalf("Init() second run error = %v", err)
	}
	if len(second.Written) != 0 || len(second.Kept) != 4 {
		t.Fatalf("Init() second run = %#v", second)
	}
}

func TestProjectInitCreatesMissingProject(t *testing.T) {
	t.Parallel()

	var created state.CreateProjectInput
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch {
		case request.URL.Path == "/api/v1/projects" && request.Method == http.MethodGet:
			_ = json.NewEncoder(writer).Encode(map[string]any{"projects": []state.Project{}})
		case request.URL.Path == "/api/v1/projects" && request.Method == http.MethodPost:
			if err := json.NewDecoder(request.Body).Decode(&created); err != nil {
				http.Error(writer, "bad input", http.StatusBadRequest)
				return
			}
			project := testProject
			project.Name = created.Name
			project.RootPathHint = created.RootPathHint
			writer.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(writer).Encode(project)
		default:
			http.NotFound(writer, request)
		}
	}))
	t.Cleanup(server.Close)

	service, profileName := newProjectFixture(t, server)
	result, err := service.Init(context.Background(), ProjectInitRequest{
		Name:        "customer-api",
		RootPath:    "~/src/customer-api",
		ProfileName: profileName,
		Dir:         t.TempDir(),
	})
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	if !result.Created || created.Name != "customer-api" || created.RootPathHint != "~/src/customer-api" || created.ClientRequestID == "" {
		t.Fatalf("Init() created = %#v, input = %#v", result.Created, created)
	}
}

func TestProjectInitExplainsOwnerGatedCreation(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		if request.Method == http.MethodGet {
			_ = json.NewEncoder(writer).Encode(map[string]any{"projects": []state.Project{}})
			return
		}
		writer.WriteHeader(http.StatusForbidden)
		_ = json.NewEncoder(writer).Encode(map[string]string{"code": "forbidden"})
	}))
	t.Cleanup(server.Close)

	service, profileName := newProjectFixture(t, server)
	_, err := service.Init(context.Background(), ProjectInitRequest{
		Name:        "customer-api",
		ProfileName: profileName,
		Dir:         t.TempDir(),
	})
	if err == nil || !strings.Contains(err.Error(), "owner-only") {
		t.Fatalf("Init() error = %v, want owner-gated guidance", err)
	}
}

func TestProjectInitRefusesForeignProjectFile(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(map[string]any{"projects": []state.Project{testProject}})
	}))
	t.Cleanup(server.Close)

	directory := t.TempDir()
	foreign := ProjectFile{SchemaVersion: 1, ProjectID: "01989f4a-bbbb-73a5-a131-3a6ef6a09cba", ProjectName: "other-project", Server: server.URL}
	encoded, err := json.Marshal(foreign)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if err := os.MkdirAll(filepath.Join(directory, ".state"), 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(directory, ".state", "project.json"), encoded, 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	service, profileName := newProjectFixture(t, server)
	_, err = service.Init(context.Background(), ProjectInitRequest{Name: "customer-api", ProfileName: profileName, Dir: directory})
	if !errors.Is(err, ErrProjectMismatch) {
		t.Fatalf("Init() error = %v, want ErrProjectMismatch", err)
	}
}

func TestProjectValidateCatchesBadPolicy(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	stateDir := filepath.Join(directory, ".state")
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	bad := `name: "Bad Name"
adapter: "no spaces allowed"
mode: reckless
allowed_capabilities:
  - deploy
  - deploy
  - invented_capability
timeout_minutes: 9999
`
	if err := os.WriteFile(filepath.Join(stateDir, "policy.yaml"), []byte(bad), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	validation, err := NewProjectService(nil, nil, nil).Validate(directory)
	if err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	joined := strings.Join(validation.Findings, "\n")
	for _, want := range []string{"name", "adapter", "mode", "deploy", "duplicated", "invented_capability", "timeout_minutes"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("findings miss %q:\n%s", want, joined)
		}
	}

	unattended := `name: nightly-review
adapter: codex
mode: unattended-low-risk
allowed_capabilities:
  - deploy
timeout_minutes: 30
`
	if err := os.WriteFile(filepath.Join(stateDir, "policy.yaml"), []byte(unattended), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	validation, err = NewProjectService(nil, nil, nil).Validate(directory)
	if err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if len(validation.Findings) != 1 || !strings.Contains(validation.Findings[0], "unattended-low-risk") {
		t.Fatalf("unattended findings = %#v", validation.Findings)
	}
}

func TestProjectValidateReportsMissingFile(t *testing.T) {
	t.Parallel()

	_, err := NewProjectService(nil, nil, nil).Validate(t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "project init") {
		t.Fatalf("Validate() error = %v, want init guidance", err)
	}
}

// TestProjectSyncWritesBriefingMarkdown boots the real MCP server and syncs
// the project context through the MCP path the CLI uses.
func TestProjectSyncWritesBriefingMarkdown(t *testing.T) {
	t.Parallel()

	app := pocketbase.NewWithConfig(pocketbase.Config{
		DefaultDataDir:   t.TempDir(),
		HideStartBanner:  true,
		DataMaxOpenConns: 1,
		DataMaxIdleConns: 1,
		AuxMaxOpenConns:  1,
		AuxMaxIdleConns:  1,
	})
	if err := app.Bootstrap(); err != nil {
		t.Fatalf("Bootstrap() error = %v", err)
	}
	t.Cleanup(func() {
		if err := app.ResetBootstrapState(); err != nil {
			t.Errorf("ResetBootstrapState() error = %v", err)
		}
	})
	seed := make([]byte, ed25519.SeedSize)
	for index := range seed {
		seed[index] = byte(index + 34)
	}
	repository, err := store.NewPocketBaseRepository(app, ed25519.NewKeyFromSeed(seed))
	if err != nil {
		t.Fatalf("NewPocketBaseRepository() error = %v", err)
	}
	authManager, err := stateauth.NewManager(app, "bootstrap-secret")
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	ownerCredential, err := authManager.BootstrapOwner(context.Background(), "bootstrap-secret", stateauth.OwnerBootstrapRequest{DisplayName: "Fabian", DeviceName: "iPhone"})
	if err != nil {
		t.Fatalf("BootstrapOwner() error = %v", err)
	}
	owner, err := authManager.Authenticate(context.Background(), ownerCredential.Token)
	if err != nil {
		t.Fatalf("Authenticate() error = %v", err)
	}
	service := state.NewService(repository)
	handler := mcpserver.NewHandler(mcpserver.Config{Auth: authManager, State: service, Version: "test-version"})

	pairing, err := authManager.CreatePairingCode(context.Background(), owner, stateauth.PairingCodeRequest{Harness: "codex", DisplayName: "Codex"})
	if err != nil {
		t.Fatalf("CreatePairingCode() error = %v", err)
	}
	credential, err := authManager.ExchangePairingCode(context.Background(), pairing.Code)
	if err != nil {
		t.Fatalf("ExchangePairingCode() error = %v", err)
	}
	if _, err := service.CreateReminder(context.Background(), owner, state.CreateReminderInput{
		Title:           "Review the nightly metrics",
		Description:     "All checks must pass",
		ClientRequestID: uuid.NewString(),
	}); err != nil {
		t.Fatalf("CreateReminder() error = %v", err)
	}

	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	configStore := NewConfigStore(filepath.Join(t.TempDir(), "statectl.json"))
	profile := Profile{Name: "codex", ServerURL: server.URL, ActorID: credential.Actor.ID, Harness: "codex"}
	if err := configStore.SaveProfile(profile); err != nil {
		t.Fatalf("SaveProfile() error = %v", err)
	}
	secrets := &memorySecretStore{values: map[string]string{profile.CredentialAccount(): credential.Token}}
	projects := NewProjectService(configStore, secrets, server.Client())

	directory := t.TempDir()
	projectFile := ProjectFile{SchemaVersion: 1, ProjectID: testProject.ID, ProjectName: "customer-api", Server: server.URL}
	encoded, err := json.Marshal(projectFile)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if err := os.MkdirAll(filepath.Join(directory, ".state"), 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(directory, ".state", "project.json"), encoded, 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	path, err := projects.Sync(context.Background(), ProjectSyncRequest{ProfileName: "codex", Dir: directory, ClientVersion: "test"})
	if err != nil {
		t.Fatalf("Sync() error = %v", err)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	text := string(contents)
	if !strings.Contains(text, "customer-api") || !strings.Contains(text, "Review the nightly metrics") {
		t.Fatalf("current.md misses project or reminder:\n%s", text)
	}
	if strings.Contains(text, credential.Token) {
		t.Fatalf("current.md contains credential material:\n%s", text)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("current.md mode = %v, %v", info.Mode(), err)
	}
}

func TestProjectSyncRequiresInitFirst(t *testing.T) {
	t.Parallel()

	service, profileName := newProjectFixture(t, httptest.NewServer(http.NotFoundHandler()))
	_, err := service.Sync(context.Background(), ProjectSyncRequest{ProfileName: profileName, Dir: t.TempDir()})
	if err == nil || !strings.Contains(err.Error(), "project init") {
		t.Fatalf("Sync() error = %v, want init guidance", err)
	}
}
