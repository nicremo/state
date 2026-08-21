package state

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

var executionNow = time.Date(2026, time.August, 21, 12, 0, 0, 0, time.UTC)

func executionService(t *testing.T) (*Service, *MemoryRepository, func(time.Time)) {
	t.Helper()
	repository := NewMemoryRepository()
	current := executionNow
	var mu sync.Mutex
	service := NewService(repository, WithClock(func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		return current
	}))
	advance := func(next time.Time) {
		mu.Lock()
		defer mu.Unlock()
		current = next
	}
	return service, repository, advance
}

func executionActors() (Actor, Actor, Actor) {
	owner := Actor{ID: "01989d9b-b5c4-7aa9-a48d-a31e76535cd7", Kind: ActorKindOwner, DisplayName: "Fabian"}
	runner := Actor{ID: "01989d9b-b5c4-7ba9-b499-5659888080b9", Kind: ActorKindRunner, DisplayName: "Mac mini"}
	secondRunner := Actor{ID: "01989d9b-b5c4-7cf9-8709-a6caec3d8f13", Kind: ActorKindRunner, DisplayName: "Studio"}
	return owner, runner, secondRunner
}

func requestID() string {
	return uuid.NewString()
}

func mustCreateProject(t *testing.T, service *Service, actor Actor, name string) Project {
	t.Helper()
	project, err := service.CreateProject(context.Background(), actor, CreateProjectInput{
		Name:            name,
		Description:     "Customer facing API",
		RootPathHint:    "~/src/" + name,
		ClientRequestID: requestID(),
	})
	if err != nil {
		t.Fatalf("CreateProject(%q) error = %v", name, err)
	}
	return project
}

func mustCreatePolicy(t *testing.T, service *Service, actor Actor, projectID string, mutate func(*CreatePolicyInput)) ExecutionPolicy {
	t.Helper()
	input := CreatePolicyInput{
		Name:                        "nightly-review",
		ProjectID:                   projectID,
		Adapter:                     "codex",
		Mode:                        ExecutionModeSupervised,
		AllowedCapabilities:         []string{CapabilityReadRepository, CapabilityRunTests},
		MarkOccurrenceDoneOnSuccess: true,
		NotifyOnCompletion:          true,
		NotifyOnFailure:             true,
		TimeoutMinutes:              30,
		ClientRequestID:             requestID(),
	}
	if mutate != nil {
		mutate(&input)
	}
	policy, err := service.CreatePolicy(context.Background(), actor, input)
	if err != nil {
		t.Fatalf("CreatePolicy() error = %v", err)
	}
	return policy
}

func mustRegisterRunner(t *testing.T, service *Service, actor Actor, projects []string, adapters []string) Runner {
	t.Helper()
	runner, err := service.RegisterRunner(context.Background(), actor, RegisterRunnerInput{
		DisplayName:     actor.DisplayName,
		Projects:        projects,
		Adapters:        adapters,
		ClientRequestID: requestID(),
	})
	if err != nil {
		t.Fatalf("RegisterRunner() error = %v", err)
	}
	return runner
}

// mustCreateExecutableReminder creates a reminder whose single occurrence is
// already due at the fixture clock and which carries the given policy.
func mustCreateExecutableReminder(t *testing.T, service *Service, actor Actor, policyID *string) Reminder {
	t.Helper()
	reminder, err := service.CreateReminder(context.Background(), actor, CreateReminderInput{
		Title:             "Review the nightly metrics",
		Description:       "All checks must pass",
		ExecutionPolicyID: policyID,
		ClientRequestID:   requestID(),
		Schedule: &Schedule{
			LocalDate: "2026-08-21",
			LocalTime: "10:00",
			TimeZone:  "UTC",
			Mode:      TimeZoneModeFixed,
		},
	})
	if err != nil {
		t.Fatalf("CreateReminder() error = %v", err)
	}
	return reminder
}

func mustMaterializeOne(t *testing.T, service *Service, now time.Time) AgentRun {
	t.Helper()
	created, err := service.MaterializeEligibleRuns(context.Background(), now)
	if err != nil {
		t.Fatalf("MaterializeEligibleRuns() error = %v", err)
	}
	if len(created) != 1 {
		t.Fatalf("MaterializeEligibleRuns() created %d runs, want 1", len(created))
	}
	return created[0]
}

func mustClaim(t *testing.T, service *Service, runner Actor) AgentRun {
	t.Helper()
	run, err := service.ClaimAgentRun(context.Background(), runner, ClaimRunInput{})
	if err != nil {
		t.Fatalf("ClaimAgentRun() error = %v", err)
	}
	return run
}

func mustReportStarted(t *testing.T, service *Service, runner Actor, run AgentRun) AgentRun {
	t.Helper()
	updated, err := service.ReportAgentRunEvent(context.Background(), runner, ReportRunEventInput{
		RunID:            run.ID,
		Event:            RunEventStarted,
		ExpectedRevision: run.Revision,
	})
	if err != nil {
		t.Fatalf("ReportAgentRunEvent(started) error = %v", err)
	}
	return updated
}

func TestCreatePolicyValidatesConfiguration(t *testing.T) {
	t.Parallel()

	service, _, _ := executionService(t)
	owner, runner, _ := executionActors()
	project := mustCreateProject(t, service, owner, "customer-api")

	cases := []struct {
		name    string
		mutate  func(*CreatePolicyInput)
		wantErr error
	}{
		{"supervised allows destructive", func(input *CreatePolicyInput) {
			input.AllowedCapabilities = []string{CapabilityDestructive, CapabilityNetworkAccess}
		}, nil},
		{"unattended allows the low-risk set", func(input *CreatePolicyInput) {
			input.Mode = ExecutionModeUnattendedLowRisk
			input.AllowedCapabilities = []string{CapabilityReadRepository, CapabilityReadStateContext, CapabilityRunTests, CapabilityEditRepository}
		}, nil},
		{"unattended rejects network access", func(input *CreatePolicyInput) {
			input.Mode = ExecutionModeUnattendedLowRisk
			input.AllowedCapabilities = []string{CapabilityReadRepository, CapabilityNetworkAccess}
		}, ErrPolicyViolation},
		{"unattended rejects write state", func(input *CreatePolicyInput) {
			input.Mode = ExecutionModeUnattendedLowRisk
			input.AllowedCapabilities = []string{CapabilityWriteState}
		}, ErrPolicyViolation},
		{"unattended rejects deploy", func(input *CreatePolicyInput) {
			input.Mode = ExecutionModeUnattendedLowRisk
			input.AllowedCapabilities = []string{CapabilityDeploy}
		}, ErrPolicyViolation},
		{"unattended rejects external messages", func(input *CreatePolicyInput) {
			input.Mode = ExecutionModeUnattendedLowRisk
			input.AllowedCapabilities = []string{CapabilityMessageExternal}
		}, ErrPolicyViolation},
		{"unattended rejects destructive", func(input *CreatePolicyInput) {
			input.Mode = ExecutionModeUnattendedLowRisk
			input.AllowedCapabilities = []string{CapabilityDestructive}
		}, ErrPolicyViolation},
		{"unknown capability", func(input *CreatePolicyInput) {
			input.AllowedCapabilities = []string{"read_everything"}
		}, ErrInvalidInput},
		{"duplicate capability", func(input *CreatePolicyInput) {
			input.AllowedCapabilities = []string{CapabilityRunTests, CapabilityRunTests}
		}, ErrInvalidInput},
		{"bad slug", func(input *CreatePolicyInput) {
			input.Name = "Nightly Review"
		}, ErrInvalidInput},
		{"bad adapter", func(input *CreatePolicyInput) {
			input.Adapter = "Codex!"
		}, ErrInvalidInput},
		{"bad mode", func(input *CreatePolicyInput) {
			input.Mode = "yolo"
		}, ErrInvalidInput},
		{"timeout below bound", func(input *CreatePolicyInput) {
			input.TimeoutMinutes = 0
		}, ErrInvalidInput},
		{"timeout above bound", func(input *CreatePolicyInput) {
			input.TimeoutMinutes = 241
		}, ErrInvalidInput},
		{"missing project", func(input *CreatePolicyInput) {
			input.ProjectID = "01989d9b-b5c4-7000-8000-0000000000ff"
		}, ErrInvalidInput},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			input := CreatePolicyInput{
				Name:                strings.ReplaceAll(testCase.name, " ", "-"),
				ProjectID:           project.ID,
				Adapter:             "codex",
				Mode:                ExecutionModeSupervised,
				AllowedCapabilities: []string{CapabilityReadRepository},
				TimeoutMinutes:      30,
				ClientRequestID:     requestID(),
			}
			testCase.mutate(&input)
			_, err := service.CreatePolicy(context.Background(), owner, input)
			if testCase.wantErr == nil {
				if err != nil {
					t.Fatalf("CreatePolicy() error = %v", err)
				}
				return
			}
			if !errors.Is(err, testCase.wantErr) {
				t.Fatalf("CreatePolicy() error = %v, want %v", err, testCase.wantErr)
			}
		})
	}

	_, err := service.CreatePolicy(context.Background(), runner, CreatePolicyInput{
		Name:            "runner-forged",
		ProjectID:       project.ID,
		Adapter:         "codex",
		Mode:            ExecutionModeSupervised,
		TimeoutMinutes:  30,
		ClientRequestID: requestID(),
	})
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("runner CreatePolicy() error = %v, want ErrForbidden", err)
	}
}

func TestProjectAndPolicyCrud(t *testing.T) {
	t.Parallel()

	service, _, _ := executionService(t)
	owner, runner, _ := executionActors()
	device := Actor{ID: "01989d9b-b5c4-7d84-a5ec-dc3821c97155", Kind: ActorKindDevice}

	_, err := service.CreateProject(context.Background(), runner, CreateProjectInput{
		Name:            "customer-api",
		ClientRequestID: requestID(),
	})
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("runner CreateProject() error = %v, want ErrForbidden", err)
	}
	_, err = service.CreateProject(context.Background(), device, CreateProjectInput{
		Name:            "customer-api",
		ClientRequestID: requestID(),
	})
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("device CreateProject() error = %v, want ErrForbidden", err)
	}

	project := mustCreateProject(t, service, owner, "customer-api")
	if project.Revision != 1 || project.CreatedAt != executionNow {
		t.Fatalf("unexpected project: %#v", project)
	}
	if _, err := service.CreateProject(context.Background(), owner, CreateProjectInput{
		Name:            "customer-api",
		ClientRequestID: requestID(),
	}); err == nil {
		t.Fatal("duplicate project name must fail")
	}

	description := "Handles customer traffic"
	updated, err := service.UpdateProject(context.Background(), owner, project.ID, UpdateProjectInput{
		Description:      &description,
		ExpectedRevision: 1,
		ClientRequestID:  requestID(),
	})
	if err != nil {
		t.Fatalf("UpdateProject() error = %v", err)
	}
	if updated.Revision != 2 || updated.Description != description {
		t.Fatalf("unexpected updated project: %#v", updated)
	}
	if _, err := service.UpdateProject(context.Background(), owner, project.ID, UpdateProjectInput{
		Description:      &description,
		ExpectedRevision: 1,
		ClientRequestID:  requestID(),
	}); !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("stale UpdateProject() error = %v, want ErrRevisionConflict", err)
	}

	projects, err := service.ListProjects(context.Background())
	if err != nil || len(projects) != 1 {
		t.Fatalf("ListProjects() = %#v, %v", projects, err)
	}

	policy := mustCreatePolicy(t, service, owner, project.ID, nil)
	if !policy.Enabled || policy.Revision != 1 {
		t.Fatalf("unexpected policy: %#v", policy)
	}

	timeout := 45
	updatedPolicy, err := service.UpdatePolicy(context.Background(), owner, policy.ID, UpdatePolicyInput{
		TimeoutMinutes:   &timeout,
		ExpectedRevision: 1,
		ClientRequestID:  requestID(),
	})
	if err != nil {
		t.Fatalf("UpdatePolicy() error = %v", err)
	}
	if updatedPolicy.Revision != 2 || updatedPolicy.TimeoutMinutes != 45 {
		t.Fatalf("unexpected updated policy: %#v", updatedPolicy)
	}

	disabled, err := service.UpdatePolicy(context.Background(), owner, policy.ID, UpdatePolicyInput{
		Enabled:          pointer(false),
		ExpectedRevision: 2,
		ClientRequestID:  requestID(),
	})
	if err != nil {
		t.Fatalf("UpdatePolicy(disable) error = %v", err)
	}
	if disabled.Enabled {
		t.Fatal("policy must be disabled")
	}
	events, err := service.ListChanges(context.Background(), 0, 500)
	if err != nil {
		t.Fatalf("ListChanges() error = %v", err)
	}
	var lastPolicyEvent AuditEvent
	for _, change := range events {
		if strings.HasPrefix(string(change.Event.Action), "policy.") {
			lastPolicyEvent = change.Event
		}
	}
	if lastPolicyEvent.Action != AuditActionPolicyDisabled {
		t.Fatalf("last policy action = %q, want %q", lastPolicyEvent.Action, AuditActionPolicyDisabled)
	}

	// Tightening the mode while a destructive capability is attached is a
	// policy violation, not a plain validation error.
	_, err = service.UpdatePolicy(context.Background(), owner, policy.ID, UpdatePolicyInput{
		AllowedCapabilities: pointer([]string{CapabilityReadRepository, CapabilityNetworkAccess}),
		Mode:                pointer(ExecutionModeUnattendedLowRisk),
		ExpectedRevision:    3,
		ClientRequestID:     requestID(),
	})
	if !errors.Is(err, ErrPolicyViolation) {
		t.Fatalf("UpdatePolicy(unattended + network) error = %v, want ErrPolicyViolation", err)
	}
}

func TestRegisterAndUpdateRunner(t *testing.T) {
	t.Parallel()

	service, _, _ := executionService(t)
	owner, runner, _ := executionActors()
	project := mustCreateProject(t, service, owner, "customer-api")

	harness := Actor{ID: "01989d9b-b5c4-7187-8ea7-68f05ecdc3d9", Kind: ActorKindHarness}
	_, err := service.RegisterRunner(context.Background(), harness, RegisterRunnerInput{
		DisplayName:     "Not a runner",
		ClientRequestID: requestID(),
	})
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("harness RegisterRunner() error = %v, want ErrForbidden", err)
	}

	registered := mustRegisterRunner(t, service, runner, []string{project.ID}, []string{"codex"})
	if registered.Revision != 1 || registered.LastSeenAt != executionNow || registered.RegisteredAt != executionNow {
		t.Fatalf("unexpected runner: %#v", registered)
	}
	if registered.ID != runner.ID {
		t.Fatalf("runner ID = %q, want actor ID %q", registered.ID, runner.ID)
	}

	reregistered, err := service.RegisterRunner(context.Background(), runner, RegisterRunnerInput{
		DisplayName:     "Mac mini",
		Projects:        []string{project.ID},
		Adapters:        []string{"codex", "claude-code"},
		ClientRequestID: requestID(),
	})
	if err != nil {
		t.Fatalf("second RegisterRunner() error = %v", err)
	}
	if reregistered.Revision != 2 || len(reregistered.Adapters) != 2 {
		t.Fatalf("unexpected re-registered runner: %#v", reregistered)
	}

	_, err = service.RegisterRunner(context.Background(), runner, RegisterRunnerInput{
		DisplayName:     "Mac mini",
		Projects:        []string{"01989d9b-b5c4-7000-8000-0000000000ff"},
		ClientRequestID: requestID(),
	})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("RegisterRunner(bad project) error = %v, want ErrInvalidInput", err)
	}
	_, err = service.RegisterRunner(context.Background(), runner, RegisterRunnerInput{
		DisplayName:     "Mac mini",
		Adapters:        []string{"Codex"},
		ClientRequestID: requestID(),
	})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("RegisterRunner(bad adapter) error = %v, want ErrInvalidInput", err)
	}

	_, err = service.UpdateRunner(context.Background(), runner, runner.ID, UpdateRunnerInput{
		DisplayName:      pointer("Renamed"),
		ExpectedRevision: 2,
		ClientRequestID:  requestID(),
	})
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("runner UpdateRunner() error = %v, want ErrForbidden", err)
	}
	updated, err := service.UpdateRunner(context.Background(), owner, runner.ID, UpdateRunnerInput{
		DisplayName:      pointer("Renamed"),
		ExpectedRevision: 2,
		ClientRequestID:  requestID(),
	})
	if err != nil {
		t.Fatalf("owner UpdateRunner() error = %v", err)
	}
	if updated.DisplayName != "Renamed" || updated.Revision != 3 {
		t.Fatalf("unexpected updated runner: %#v", updated)
	}

	runners, err := service.ListRunners(context.Background())
	if err != nil || len(runners) != 1 {
		t.Fatalf("ListRunners() = %#v, %v", runners, err)
	}

	events, err := service.ListChanges(context.Background(), 0, 500)
	if err != nil {
		t.Fatalf("ListChanges() error = %v", err)
	}
	actions := make(map[AuditAction]int)
	for _, change := range events {
		actions[change.Event.Action]++
		if strings.HasPrefix(string(change.Event.Action), "runner.") && change.Event.ReminderID != "" {
			t.Fatalf("runner event %q must not carry a reminder ID", change.Event.Action)
		}
	}
	if actions[AuditActionRunnerRegistered] != 1 || actions[AuditActionRunnerUpdated] != 2 {
		t.Fatalf("runner audit actions = %#v", actions)
	}
}

func TestCreateManualRunIsIdempotentAndLinked(t *testing.T) {
	t.Parallel()

	service, repository, _ := executionService(t)
	owner, runner, _ := executionActors()
	project := mustCreateProject(t, service, owner, "customer-api")
	policy := mustCreatePolicy(t, service, owner, project.ID, nil)
	reminder := mustCreateExecutableReminder(t, service, owner, &policy.ID)

	_, err := service.CreateManualRun(context.Background(), runner, CreateManualRunInput{
		ReminderID:       reminder.ID,
		PolicyID:         policy.ID,
		MutationMetadata: MutationMetadata{ClientRequestID: requestID()},
	})
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("runner CreateManualRun() error = %v, want ErrForbidden", err)
	}

	input := CreateManualRunInput{
		ReminderID:       reminder.ID,
		PolicyID:         policy.ID,
		MutationMetadata: MutationMetadata{ClientRequestID: requestID()},
	}
	run, err := service.CreateManualRun(context.Background(), owner, input)
	if err != nil {
		t.Fatalf("CreateManualRun() error = %v", err)
	}
	if run.Status != AgentRunStatusEligible || run.OccurrenceID != nil || run.ReminderID != reminder.ID {
		t.Fatalf("unexpected run: %#v", run)
	}
	if run.PolicyRevision != policy.Revision || run.Adapter != policy.Adapter || run.ProjectID != project.ID {
		t.Fatalf("run does not pin the policy: %#v", run)
	}
	if run.TaskContract.ContractHash == "" || run.TaskContract.ComputeHash() != run.TaskContract.ContractHash {
		t.Fatal("task contract hash does not verify")
	}
	if run.TaskContract.Objective != reminder.Title || run.TaskContract.CorrelationID != run.ID {
		t.Fatalf("unexpected task contract: %#v", run.TaskContract)
	}

	replayed, err := service.CreateManualRun(context.Background(), owner, input)
	if err != nil {
		t.Fatalf("replayed CreateManualRun() error = %v", err)
	}
	if replayed.ID != run.ID {
		t.Fatalf("replayed run ID = %q, want %q", replayed.ID, run.ID)
	}
	events, err := repository.ListAuditEvents(context.Background(), reminder.ID)
	if err != nil {
		t.Fatalf("ListAuditEvents() error = %v", err)
	}
	if len(events) != 2 { // reminder.created + run.eligible
		t.Fatalf("audit event count = %d, want 2", len(events))
	}
	runEvent := events[1]
	if runEvent.Action != AuditActionRunEligible || runEvent.CorrelationID != run.ID {
		t.Fatalf("unexpected run audit event: %#v", runEvent)
	}
}

func TestMaterializeEligibleRunsDeduplicatesCycles(t *testing.T) {
	t.Parallel()

	service, repository, _ := executionService(t)
	owner, _, _ := executionActors()
	project := mustCreateProject(t, service, owner, "customer-api")
	policy := mustCreatePolicy(t, service, owner, project.ID, nil)
	reminder := mustCreateExecutableReminder(t, service, owner, &policy.ID)
	mustCreateExecutableReminder(t, service, owner, nil) // no policy attached: never materializes

	// Not due yet: nothing materializes before the fire time.
	created, err := service.MaterializeEligibleRuns(context.Background(), executionNow.Add(-3*time.Hour))
	if err != nil {
		t.Fatalf("MaterializeEligibleRuns() error = %v", err)
	}
	if len(created) != 0 {
		t.Fatalf("early materialization created %d runs, want 0", len(created))
	}

	run := mustMaterializeOne(t, service, executionNow)
	if run.Status != AgentRunStatusEligible || run.OccurrenceID == nil {
		t.Fatalf("unexpected materialized run: %#v", run)
	}
	if run.CreatedByActor.Kind != ActorKindSystem || run.ContextCursor <= 0 {
		t.Fatalf("unexpected materialization metadata: %#v", run)
	}

	// A repeated scheduler cycle materializes nothing twice.
	again, err := service.MaterializeEligibleRuns(context.Background(), executionNow)
	if err != nil {
		t.Fatalf("second MaterializeEligibleRuns() error = %v", err)
	}
	if len(again) != 0 {
		t.Fatalf("repeated cycle created %d runs, want 0", len(again))
	}

	events, err := repository.ListAuditEvents(context.Background(), reminder.ID)
	if err != nil {
		t.Fatalf("ListAuditEvents() error = %v", err)
	}
	runEvents := 0
	for _, event := range events {
		if event.Action == AuditActionRunEligible {
			runEvents++
			if event.Actor.Kind != ActorKindSystem {
				t.Fatalf("materialization actor = %q, want system", event.Actor.Kind)
			}
		}
	}
	if runEvents != 1 {
		t.Fatalf("run.eligible event count = %d, want 1", runEvents)
	}

	// A disabled policy materializes nothing.
	secondReminder := mustCreateExecutableReminder(t, service, owner, &policy.ID)
	if _, err := service.UpdatePolicy(context.Background(), owner, policy.ID, UpdatePolicyInput{
		Enabled:          pointer(false),
		ExpectedRevision: policy.Revision,
		ClientRequestID:  requestID(),
	}); err != nil {
		t.Fatalf("UpdatePolicy(disable) error = %v", err)
	}
	created, err = service.MaterializeEligibleRuns(context.Background(), executionNow)
	if err != nil {
		t.Fatalf("MaterializeEligibleRuns(disabled) error = %v", err)
	}
	if len(created) != 0 {
		t.Fatalf("disabled policy created %d runs, want 0", len(created))
	}
	runs, err := service.ListAgentRuns(context.Background(), AgentRunListFilter{ReminderID: secondReminder.ID})
	if err != nil || len(runs) != 0 {
		t.Fatalf("ListAgentRuns() = %#v, %v", runs, err)
	}
}

func TestClaimAgentRunIsAtomic(t *testing.T) {
	t.Parallel()

	service, repository, _ := executionService(t)
	owner, runner, secondRunner := executionActors()
	project := mustCreateProject(t, service, owner, "customer-api")
	policy := mustCreatePolicy(t, service, owner, project.ID, nil)
	reminder := mustCreateExecutableReminder(t, service, owner, &policy.ID)
	mustRegisterRunner(t, service, runner, []string{project.ID}, []string{"codex"})
	mustRegisterRunner(t, service, secondRunner, []string{project.ID}, []string{"codex"})
	materialized := mustMaterializeOne(t, service, executionNow)

	type claimResult struct {
		run AgentRun
		err error
	}
	results := make(chan claimResult, 2)
	var wait sync.WaitGroup
	for _, actor := range []Actor{runner, secondRunner} {
		wait.Add(1)
		go func(actor Actor) {
			defer wait.Done()
			run, err := service.ClaimAgentRun(context.Background(), actor, ClaimRunInput{})
			results <- claimResult{run: run, err: err}
		}(actor)
	}
	wait.Wait()
	close(results)

	var claimed *AgentRun
	losers := 0
	for result := range results {
		if result.err == nil {
			if claimed != nil {
				t.Fatal("two runners claimed the same run")
			}
			claim := result.run
			claimed = &claim
			continue
		}
		if !errors.Is(result.err, ErrNotClaimable) {
			t.Fatalf("losing claim error = %v, want ErrNotClaimable", result.err)
		}
		losers++
	}
	if claimed == nil || losers != 1 {
		t.Fatalf("claimed=%v losers=%d, want exactly one of each", claimed != nil, losers)
	}
	if claimed.ID != materialized.ID {
		t.Fatalf("claimed run = %q, want materialized %q", claimed.ID, materialized.ID)
	}
	if claimed.Status != AgentRunStatusClaimed || claimed.RunnerID == nil || claimed.Revision != 2 {
		t.Fatalf("unexpected claimed run: %#v", claimed)
	}
	wantLease := executionNow.Add(60 * time.Minute) // 2 * timeout (30m)
	if claimed.LeaseExpiresAt == nil || !claimed.LeaseExpiresAt.Equal(wantLease) {
		t.Fatalf("lease = %v, want %v", claimed.LeaseExpiresAt, wantLease)
	}
	if claimed.ClaimedAt == nil || !claimed.ClaimedAt.Equal(executionNow) {
		t.Fatalf("claimed at = %v, want %v", claimed.ClaimedAt, executionNow)
	}

	events, err := repository.ListAuditEvents(context.Background(), reminder.ID)
	if err != nil {
		t.Fatalf("ListAuditEvents() error = %v", err)
	}
	claimEvents := 0
	for _, event := range events {
		if event.Action == AuditActionRunClaimed {
			claimEvents++
		}
	}
	if claimEvents != 1 {
		t.Fatalf("run.claimed event count = %d, want 1", claimEvents)
	}

	// The claimed run is no longer claimable.
	_, err = service.ClaimAgentRun(context.Background(), runner, ClaimRunInput{})
	if !errors.Is(err, ErrNotClaimable) {
		t.Fatalf("second ClaimAgentRun() error = %v, want ErrNotClaimable", err)
	}
}

func TestClaimAgentRunLongPollAndScope(t *testing.T) {
	t.Parallel()

	service, _, _ := executionService(t)
	owner, runner, _ := executionActors()
	project := mustCreateProject(t, service, owner, "customer-api")
	policy := mustCreatePolicy(t, service, owner, project.ID, nil)
	reminder := mustCreateExecutableReminder(t, service, owner, &policy.ID)
	registered := mustRegisterRunner(t, service, runner, []string{project.ID}, []string{"codex"})

	// A runner that covers neither project nor adapter finds nothing.
	outsider := Actor{ID: "01989d9b-b5c4-7104-b62d-b9ec26b940d4", Kind: ActorKindRunner, DisplayName: "Outsider"}
	mustRegisterRunner(t, service, outsider, []string{project.ID}, []string{"opencode"})
	_, err := service.ClaimAgentRun(context.Background(), outsider, ClaimRunInput{})
	if !errors.Is(err, ErrNotClaimable) {
		t.Fatalf("outsider ClaimAgentRun() error = %v, want ErrNotClaimable", err)
	}

	// An unregistered runner has no profile to claim with.
	unregistered := Actor{ID: "01989d9b-b5c4-7484-a36d-351efcfe1f16", Kind: ActorKindRunner}
	_, err = service.ClaimAgentRun(context.Background(), unregistered, ClaimRunInput{})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("unregistered ClaimAgentRun() error = %v, want ErrNotFound", err)
	}

	// Owner and harness kinds never claim.
	_, err = service.ClaimAgentRun(context.Background(), owner, ClaimRunInput{})
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("owner ClaimAgentRun() error = %v, want ErrForbidden", err)
	}

	// The long poll picks up a run that appears while waiting.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	type claimResult struct {
		run AgentRun
		err error
	}
	resultChannel := make(chan claimResult, 1)
	go func() {
		run, err := service.ClaimAgentRun(ctx, runner, ClaimRunInput{WaitSeconds: 25})
		resultChannel <- claimResult{run: run, err: err}
	}()
	time.Sleep(300 * time.Millisecond)
	if _, err := service.CreateManualRun(context.Background(), owner, CreateManualRunInput{
		ReminderID:       reminder.ID,
		PolicyID:         policy.ID,
		MutationMetadata: MutationMetadata{ClientRequestID: requestID()},
	}); err != nil {
		t.Fatalf("CreateManualRun() error = %v", err)
	}
	select {
	case result := <-resultChannel:
		if result.err != nil {
			t.Fatalf("long-poll ClaimAgentRun() error = %v", result.err)
		}
		if result.run.RunnerID == nil || *result.run.RunnerID != registered.ID {
			t.Fatalf("long-poll claimed run: %#v", result.run)
		}
	case <-ctx.Done():
		t.Fatal("long-poll claim did not pick up the manual run")
	}

	// The waiter saw the runner profile: last-seen moved past registration is
	// tracked at claim time.
	profile, err := service.GetRunner(context.Background(), runner.ID)
	if err != nil {
		t.Fatalf("GetRunner() error = %v", err)
	}
	if profile.LastSeenAt.Before(executionNow) {
		t.Fatalf("last seen = %v, want at least %v", profile.LastSeenAt, executionNow)
	}
}

func TestReportAgentRunEventLifecycle(t *testing.T) {
	t.Parallel()

	service, repository, advance := executionService(t)
	owner, runner, secondRunner := executionActors()
	project := mustCreateProject(t, service, owner, "customer-api")
	policy := mustCreatePolicy(t, service, owner, project.ID, nil)
	reminder := mustCreateExecutableReminder(t, service, owner, &policy.ID)
	mustRegisterRunner(t, service, runner, []string{project.ID}, []string{"codex"})
	mustRegisterRunner(t, service, secondRunner, []string{project.ID}, []string{"codex"})
	mustMaterializeOne(t, service, executionNow)
	claimed := mustClaim(t, service, runner)

	_, err := service.ReportAgentRunEvent(context.Background(), secondRunner, ReportRunEventInput{
		RunID:            claimed.ID,
		Event:            RunEventStarted,
		ExpectedRevision: claimed.Revision,
	})
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("foreign runner report error = %v, want ErrForbidden", err)
	}

	running := mustReportStarted(t, service, runner, claimed)
	if running.Status != AgentRunStatusRunning || running.StartedAt == nil || running.Revision != 3 {
		t.Fatalf("unexpected running run: %#v", running)
	}

	// started is a one-way transition.
	_, err = service.ReportAgentRunEvent(context.Background(), runner, ReportRunEventInput{
		RunID:            running.ID,
		Event:            RunEventStarted,
		ExpectedRevision: running.Revision,
	})
	if !errors.Is(err, ErrRunStateConflict) {
		t.Fatalf("second started error = %v, want ErrRunStateConflict", err)
	}

	progressed, err := service.ReportAgentRunEvent(context.Background(), runner, ReportRunEventInput{
		RunID:            running.ID,
		Event:            RunEventProgress,
		Detail:           "half the checks passed",
		ExpectedRevision: running.Revision,
	})
	if err != nil {
		t.Fatalf("ReportAgentRunEvent(progress) error = %v", err)
	}
	if progressed.Status != AgentRunStatusRunning || progressed.Revision != 4 {
		t.Fatalf("unexpected progressed run: %#v", progressed)
	}

	eventsBeforeHeartbeat, err := repository.ListAuditEvents(context.Background(), reminder.ID)
	if err != nil {
		t.Fatalf("ListAuditEvents() error = %v", err)
	}

	// The heartbeat extends the lease without growing the audit chain.
	advance(executionNow.Add(10 * time.Minute))
	heartbeat, err := service.ReportAgentRunEvent(context.Background(), runner, ReportRunEventInput{
		RunID:            progressed.ID,
		Event:            RunEventHeartbeat,
		ExpectedRevision: progressed.Revision,
	})
	if err != nil {
		t.Fatalf("ReportAgentRunEvent(heartbeat) error = %v", err)
	}
	if heartbeat.Revision != 5 {
		t.Fatalf("heartbeat revision = %d, want 5", heartbeat.Revision)
	}
	wantLease := executionNow.Add(10 * time.Minute).Add(60 * time.Minute)
	if heartbeat.LeaseExpiresAt == nil || !heartbeat.LeaseExpiresAt.Equal(wantLease) {
		t.Fatalf("heartbeat lease = %v, want %v", heartbeat.LeaseExpiresAt, wantLease)
	}
	if heartbeat.LeaseExpiresAt.Before(*progressed.LeaseExpiresAt) || heartbeat.LeaseExpiresAt.Equal(*progressed.LeaseExpiresAt) {
		t.Fatal("heartbeat must extend the lease")
	}
	eventsAfterHeartbeat, err := repository.ListAuditEvents(context.Background(), reminder.ID)
	if err != nil {
		t.Fatalf("ListAuditEvents() error = %v", err)
	}
	if len(eventsAfterHeartbeat) != len(eventsBeforeHeartbeat) {
		t.Fatalf("heartbeat grew the audit chain: %d -> %d", len(eventsBeforeHeartbeat), len(eventsAfterHeartbeat))
	}

	_, err = service.ReportAgentRunEvent(context.Background(), runner, ReportRunEventInput{
		RunID:            heartbeat.ID,
		Event:            RunEventHeartbeat,
		ExpectedRevision: progressed.Revision, // stale
	})
	if !errors.Is(err, ErrRunStateConflict) {
		t.Fatalf("stale heartbeat error = %v, want ErrRunStateConflict", err)
	}

	_, err = service.ReportAgentRunEvent(context.Background(), runner, ReportRunEventInput{
		RunID:            heartbeat.ID,
		Event:            "explode",
		ExpectedRevision: heartbeat.Revision,
	})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("unknown event error = %v, want ErrInvalidInput", err)
	}

	var sawProgress bool
	for _, event := range eventsBeforeHeartbeat {
		if event.Action == AuditActionRunProgress {
			sawProgress = true
			if event.SourceExcerpt != "half the checks passed" {
				t.Fatalf("progress excerpt = %q", event.SourceExcerpt)
			}
		}
	}
	if !sawProgress {
		t.Fatal("run.progress event missing")
	}
}

func TestCompleteAgentRunAutoCompletesOccurrence(t *testing.T) {
	t.Parallel()

	service, repository, _ := executionService(t)
	owner, runner, _ := executionActors()
	project := mustCreateProject(t, service, owner, "customer-api")
	policy := mustCreatePolicy(t, service, owner, project.ID, nil)
	reminder := mustCreateExecutableReminder(t, service, owner, &policy.ID)
	mustRegisterRunner(t, service, runner, []string{project.ID}, []string{"codex"})
	materialized := mustMaterializeOne(t, service, executionNow)

	type notification struct {
		run   AgentRun
		title string
	}
	var notifications []notification
	service.runNotifier = func(_ context.Context, run AgentRun, reminderTitle string) error {
		notifications = append(notifications, notification{run: run, title: reminderTitle})
		return nil
	}

	claimed := mustClaim(t, service, runner)
	running := mustReportStarted(t, service, runner, claimed)
	completed, err := service.CompleteAgentRun(context.Background(), runner, CompleteRunInput{
		RunID:            running.ID,
		Outcome:          AgentRunStatusSucceeded,
		ResultSummary:    "all checks passed\napi_token=hunter2",
		ExpectedRevision: running.Revision,
		MutationMetadata: MutationMetadata{ClientRequestID: requestID()},
	})
	if err != nil {
		t.Fatalf("CompleteAgentRun() error = %v", err)
	}
	if completed.Status != AgentRunStatusSucceeded || completed.FinishedAt == nil || completed.LeaseExpiresAt != nil {
		t.Fatalf("unexpected completed run: %#v", completed)
	}
	if completed.ResultSummary != "all checks passed" {
		t.Fatalf("result summary = %q, want redacted", completed.ResultSummary)
	}
	if completed.CompletedByActor == nil || completed.CompletedByActor.ID != runner.ID {
		t.Fatalf("completed by = %#v", completed.CompletedByActor)
	}

	// The originating occurrence was completed in the same transaction.
	occurrence, err := repository.GetOccurrence(context.Background(), *materialized.OccurrenceID)
	if err != nil {
		t.Fatalf("GetOccurrence() error = %v", err)
	}
	if occurrence.Status != OccurrenceStatusCompleted || occurrence.CompletedAt == nil {
		t.Fatalf("occurrence not completed: %#v", occurrence)
	}

	events, err := repository.ListAuditEvents(context.Background(), reminder.ID)
	if err != nil {
		t.Fatalf("ListAuditEvents() error = %v", err)
	}
	actions := make([]AuditAction, 0, len(events))
	for _, event := range events {
		actions = append(actions, event.Action)
		if event.Action == AuditActionOccurrenceDone && event.CorrelationID != completed.ID {
			t.Fatalf("occurrence completion correlation = %q, want run ID %q", event.CorrelationID, completed.ID)
		}
	}
	for _, want := range []AuditAction{AuditActionRunEligible, AuditActionRunClaimed, AuditActionRunStarted, AuditActionRunSucceeded, AuditActionOccurrenceDone} {
		if !containsAction(actions, want) {
			t.Fatalf("audit actions %v miss %q", actions, want)
		}
	}
	assertChainLinked(t, service)

	if len(notifications) != 1 || notifications[0].title != reminder.Title || notifications[0].run.ID != completed.ID {
		t.Fatalf("notifications = %#v", notifications)
	}

	// A second finalization with a stale revision is rejected and records
	// nothing.
	_, err = service.CompleteAgentRun(context.Background(), runner, CompleteRunInput{
		RunID:            running.ID,
		Outcome:          AgentRunStatusSucceeded,
		ExpectedRevision: running.Revision,
		MutationMetadata: MutationMetadata{ClientRequestID: requestID()},
	})
	if !errors.Is(err, ErrRunStateConflict) {
		t.Fatalf("second CompleteAgentRun() error = %v, want ErrRunStateConflict", err)
	}
	if len(notifications) != 1 {
		t.Fatalf("notifications after conflict = %d, want 1", len(notifications))
	}
}

func TestCompleteAgentRunEvidenceMismatchForcesFailure(t *testing.T) {
	t.Parallel()

	service, repository, _ := executionService(t)
	owner, runner, _ := executionActors()
	project := mustCreateProject(t, service, owner, "customer-api")
	policy := mustCreatePolicy(t, service, owner, project.ID, nil)
	reminder := mustCreateExecutableReminder(t, service, owner, &policy.ID)
	mustRegisterRunner(t, service, runner, []string{project.ID}, []string{"codex"})

	complete := func(outcome AgentRunStatus, exitCode int) AgentRun {
		t.Helper()
		run, err := service.CreateManualRun(context.Background(), owner, CreateManualRunInput{
			ReminderID:       reminder.ID,
			PolicyID:         policy.ID,
			MutationMetadata: MutationMetadata{ClientRequestID: requestID()},
		})
		if err != nil {
			t.Fatalf("CreateManualRun() error = %v", err)
		}
		claimed := mustClaim(t, service, runner)
		if claimed.ID != run.ID {
			t.Fatalf("claimed %q, want %q", claimed.ID, run.ID)
		}
		completed, err := service.CompleteAgentRun(context.Background(), runner, CompleteRunInput{
			RunID:            claimed.ID,
			Outcome:          outcome,
			ExitCode:         exitCode,
			ExpectedRevision: claimed.Revision,
			MutationMetadata: MutationMetadata{ClientRequestID: requestID()},
		})
		if err != nil {
			t.Fatalf("CompleteAgentRun(%s, %d) error = %v", outcome, exitCode, err)
		}
		return completed
	}

	mismatchedSuccess := complete(AgentRunStatusSucceeded, 1)
	if mismatchedSuccess.Status != AgentRunStatusFailed || mismatchedSuccess.FailureCode != RunFailureEvidenceMismatch {
		t.Fatalf("succeeded+exit 1 = %#v", mismatchedSuccess)
	}

	mismatchedFailure := complete(AgentRunStatusFailed, 0)
	if mismatchedFailure.Status != AgentRunStatusFailed || mismatchedFailure.FailureCode != RunFailureEvidenceMismatch {
		t.Fatalf("failed+exit 0 = %#v", mismatchedFailure)
	}

	consistentFailure := complete(AgentRunStatusFailed, 2)
	if consistentFailure.Status != AgentRunStatusFailed || consistentFailure.FailureCode != "" {
		t.Fatalf("failed+exit 2 = %#v", consistentFailure)
	}

	// Failed runs never auto-complete the occurrence, and only one audit
	// event mentions a mismatch per run.
	occurrences, err := repository.ListOccurrences(context.Background(), reminder.ID, OccurrenceListOptions{})
	if err != nil {
		t.Fatalf("ListOccurrences() error = %v", err)
	}
	for _, occurrence := range occurrences {
		if occurrence.Status == OccurrenceStatusCompleted {
			t.Fatalf("occurrence must stay actionable: %#v", occurrence)
		}
	}
}

func TestCompleteAgentRunCallerFailureCode(t *testing.T) {
	t.Parallel()

	service, _, _ := executionService(t)
	owner, runner, _ := executionActors()
	project := mustCreateProject(t, service, owner, "customer-api")
	policy := mustCreatePolicy(t, service, owner, project.ID, nil)
	reminder := mustCreateExecutableReminder(t, service, owner, &policy.ID)
	mustRegisterRunner(t, service, runner, []string{project.ID}, []string{"codex"})

	freshClaim := func() AgentRun {
		t.Helper()
		if _, err := service.CreateManualRun(context.Background(), owner, CreateManualRunInput{
			ReminderID:       reminder.ID,
			PolicyID:         policy.ID,
			MutationMetadata: MutationMetadata{ClientRequestID: requestID()},
		}); err != nil {
			t.Fatalf("CreateManualRun() error = %v", err)
		}
		return mustClaim(t, service, runner)
	}

	claimed := freshClaim()
	completed, err := service.CompleteAgentRun(context.Background(), runner, CompleteRunInput{
		RunID:            claimed.ID,
		Outcome:          AgentRunStatusFailed,
		FailureCode:      RunFailureAdapterUnavailable,
		ExitCode:         1,
		ExpectedRevision: claimed.Revision,
		MutationMetadata: MutationMetadata{ClientRequestID: requestID()},
	})
	if err != nil {
		t.Fatalf("CompleteAgentRun() error = %v", err)
	}
	if completed.Status != AgentRunStatusFailed || completed.FailureCode != RunFailureAdapterUnavailable {
		t.Fatalf("adapter_unavailable completion = %#v", completed)
	}

	// A caller cannot forge the server-owned mismatch code.
	forged := freshClaim()
	_, err = service.CompleteAgentRun(context.Background(), runner, CompleteRunInput{
		RunID:            forged.ID,
		Outcome:          AgentRunStatusFailed,
		FailureCode:      RunFailureEvidenceMismatch,
		ExitCode:         1,
		ExpectedRevision: forged.Revision,
		MutationMetadata: MutationMetadata{ClientRequestID: requestID()},
	})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("forged failure code error = %v, want ErrInvalidInput", err)
	}

	// A failure code on a successful outcome is contradictory input.
	happy := freshClaim()
	_, err = service.CompleteAgentRun(context.Background(), runner, CompleteRunInput{
		RunID:            happy.ID,
		Outcome:          AgentRunStatusSucceeded,
		FailureCode:      RunFailureAdapterUnavailable,
		ExitCode:         0,
		ExpectedRevision: happy.Revision,
		MutationMetadata: MutationMetadata{ClientRequestID: requestID()},
	})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("failure code on success error = %v, want ErrInvalidInput", err)
	}
}

func TestApprovalFlow(t *testing.T) {
	t.Parallel()

	service, repository, _ := executionService(t)
	owner, runner, _ := executionActors()
	project := mustCreateProject(t, service, owner, "customer-api")
	policy := mustCreatePolicy(t, service, owner, project.ID, func(input *CreatePolicyInput) {
		input.AllowedCapabilities = []string{CapabilityReadRepository}
		input.NotifyOnFailure = true
	})
	reminder := mustCreateExecutableReminder(t, service, owner, &policy.ID)
	mustRegisterRunner(t, service, runner, []string{project.ID}, []string{"codex"})
	mustMaterializeOne(t, service, executionNow)
	claimed := mustClaim(t, service, runner)
	running := mustReportStarted(t, service, runner, claimed)

	var notifications []AgentRun
	service.runNotifier = func(_ context.Context, run AgentRun, _ string) error {
		notifications = append(notifications, run)
		return nil
	}

	// A capability the policy already grants never needs approval.
	_, err := service.RequestAgentApproval(context.Background(), runner, RequestApprovalInput{
		RunID:            running.ID,
		Capability:       CapabilityReadRepository,
		ExpectedRevision: running.Revision,
	})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("approval for granted capability error = %v, want ErrInvalidInput", err)
	}
	_, err = service.RequestAgentApproval(context.Background(), runner, RequestApprovalInput{
		RunID:            running.ID,
		Capability:       "teleport",
		ExpectedRevision: running.Revision,
	})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("approval for unknown capability error = %v, want ErrInvalidInput", err)
	}

	waiting, err := service.RequestAgentApproval(context.Background(), runner, RequestApprovalInput{
		RunID:            running.ID,
		Capability:       CapabilityDeploy,
		Reason:           "deploy to staging\napi_key: abc123",
		ExpectedRevision: running.Revision,
	})
	if err != nil {
		t.Fatalf("RequestAgentApproval() error = %v", err)
	}
	if waiting.Status != AgentRunStatusNeedsApproval || waiting.ApprovalCapability != CapabilityDeploy {
		t.Fatalf("unexpected waiting run: %#v", waiting)
	}

	// Only the owner decides, and only from needs_approval.
	_, err = service.ApproveAgentRun(context.Background(), runner, ApproveRunInput{
		RunID:            waiting.ID,
		Approved:         true,
		ExpectedRevision: waiting.Revision,
		MutationMetadata: MutationMetadata{ClientRequestID: requestID()},
	})
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("runner approve error = %v, want ErrForbidden", err)
	}

	approved, err := service.ApproveAgentRun(context.Background(), owner, ApproveRunInput{
		RunID:            waiting.ID,
		Approved:         true,
		ExpectedRevision: waiting.Revision,
		MutationMetadata: MutationMetadata{ClientRequestID: requestID()},
	})
	if err != nil {
		t.Fatalf("ApproveAgentRun() error = %v", err)
	}
	if approved.Status != AgentRunStatusClaimed || approved.ApprovalCapability != "" || approved.LeaseExpiresAt == nil {
		t.Fatalf("unexpected approved run: %#v", approved)
	}
	if len(notifications) != 0 {
		t.Fatalf("approval must not notify, got %#v", notifications)
	}

	// A second request needs a second approval; a decline cancels the run.
	runningAgain := mustReportStarted(t, service, runner, approved)
	waitingAgain, err := service.RequestAgentApproval(context.Background(), runner, RequestApprovalInput{
		RunID:            runningAgain.ID,
		Capability:       CapabilityNetworkAccess,
		ExpectedRevision: runningAgain.Revision,
	})
	if err != nil {
		t.Fatalf("second RequestAgentApproval() error = %v", err)
	}
	declined, err := service.ApproveAgentRun(context.Background(), owner, ApproveRunInput{
		RunID:            waitingAgain.ID,
		Approved:         false,
		ExpectedRevision: waitingAgain.Revision,
		MutationMetadata: MutationMetadata{ClientRequestID: requestID()},
	})
	if err != nil {
		t.Fatalf("decline error = %v", err)
	}
	if declined.Status != AgentRunStatusCancelled || declined.FailureCode != RunFailureApprovalDeclined || declined.FinishedAt == nil {
		t.Fatalf("unexpected declined run: %#v", declined)
	}
	if len(notifications) != 1 || notifications[0].Status != AgentRunStatusCancelled {
		t.Fatalf("decline notifications = %#v", notifications)
	}

	events, err := repository.ListAuditEvents(context.Background(), reminder.ID)
	if err != nil {
		t.Fatalf("ListAuditEvents() error = %v", err)
	}
	actions := make([]AuditAction, 0, len(events))
	for _, event := range events {
		actions = append(actions, event.Action)
		if event.Action == AuditActionRunApprovalRequested && strings.Contains(event.SourceExcerpt, "abc123") {
			t.Fatalf("approval reason leaked a secret: %q", event.SourceExcerpt)
		}
	}
	for _, want := range []AuditAction{AuditActionRunApprovalRequested, AuditActionRunApproved, AuditActionRunDeclined} {
		if !containsAction(actions, want) {
			t.Fatalf("audit actions %v miss %q", actions, want)
		}
	}
	assertChainLinked(t, service)
}

func TestCancelAgentRun(t *testing.T) {
	t.Parallel()

	service, repository, _ := executionService(t)
	owner, runner, _ := executionActors()
	project := mustCreateProject(t, service, owner, "customer-api")
	policy := mustCreatePolicy(t, service, owner, project.ID, nil)
	reminder := mustCreateExecutableReminder(t, service, owner, &policy.ID)
	mustRegisterRunner(t, service, runner, []string{project.ID}, []string{"codex"})
	mustMaterializeOne(t, service, executionNow)

	var notifications []AgentRun
	service.runNotifier = func(_ context.Context, run AgentRun, _ string) error {
		notifications = append(notifications, run)
		return nil
	}

	claimed := mustClaim(t, service, runner)
	_, err := service.CancelAgentRun(context.Background(), runner, CancelRunInput{
		RunID:            claimed.ID,
		ExpectedRevision: claimed.Revision,
		MutationMetadata: MutationMetadata{ClientRequestID: requestID()},
	})
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("runner cancel error = %v, want ErrForbidden", err)
	}

	cancelled, err := service.CancelAgentRun(context.Background(), owner, CancelRunInput{
		RunID:            claimed.ID,
		ExpectedRevision: claimed.Revision,
		MutationMetadata: MutationMetadata{ClientRequestID: requestID()},
	})
	if err != nil {
		t.Fatalf("CancelAgentRun() error = %v", err)
	}
	if cancelled.Status != AgentRunStatusCancelled || cancelled.LeaseExpiresAt != nil || cancelled.FinishedAt == nil {
		t.Fatalf("unexpected cancelled run: %#v", cancelled)
	}
	if len(notifications) != 1 || notifications[0].Status != AgentRunStatusCancelled {
		t.Fatalf("cancel notifications = %#v", notifications)
	}

	_, err = service.CancelAgentRun(context.Background(), owner, CancelRunInput{
		RunID:            claimed.ID,
		ExpectedRevision: cancelled.Revision,
		MutationMetadata: MutationMetadata{ClientRequestID: requestID()},
	})
	if !errors.Is(err, ErrRunStateConflict) {
		t.Fatalf("cancel of terminal run error = %v, want ErrRunStateConflict", err)
	}

	events, err := repository.ListAuditEvents(context.Background(), reminder.ID)
	if err != nil {
		t.Fatalf("ListAuditEvents() error = %v", err)
	}
	if !containsAction(eventActions(events), AuditActionRunCancelled) {
		t.Fatal("run.cancelled event missing")
	}
}

func TestRequeueExpiredLeases(t *testing.T) {
	t.Parallel()

	service, repository, _ := executionService(t)
	owner, runner, secondRunner := executionActors()
	project := mustCreateProject(t, service, owner, "customer-api")
	policy := mustCreatePolicy(t, service, owner, project.ID, nil)
	reminder := mustCreateExecutableReminder(t, service, owner, &policy.ID)
	mustRegisterRunner(t, service, runner, []string{project.ID}, []string{"codex"})
	mustRegisterRunner(t, service, secondRunner, []string{project.ID}, []string{"codex"})
	mustMaterializeOne(t, service, executionNow)
	claimed := mustClaim(t, service, runner)

	// Nothing to requeue before the lease lapses.
	requeued, err := service.RequeueExpiredLeases(context.Background(), executionNow.Add(30*time.Minute))
	if err != nil {
		t.Fatalf("RequeueExpiredLeases() error = %v", err)
	}
	if len(requeued) != 0 {
		t.Fatalf("early requeue returned %d runs, want 0", len(requeued))
	}

	// After the lease lapses the run goes back to eligible and another runner
	// picks it up.
	requeued, err = service.RequeueExpiredLeases(context.Background(), executionNow.Add(61*time.Minute))
	if err != nil {
		t.Fatalf("RequeueExpiredLeases() error = %v", err)
	}
	if len(requeued) != 1 {
		t.Fatalf("requeue returned %d runs, want 1", len(requeued))
	}
	requeuedRun := requeued[0]
	if requeuedRun.Status != AgentRunStatusEligible || requeuedRun.RunnerID != nil || requeuedRun.LeaseExpiresAt != nil || requeuedRun.ClaimedAt != nil {
		t.Fatalf("unexpected requeued run: %#v", requeuedRun)
	}
	if requeuedRun.Revision != claimed.Revision+1 {
		t.Fatalf("requeued revision = %d, want %d", requeuedRun.Revision, claimed.Revision+1)
	}

	events, err := repository.ListAuditEvents(context.Background(), reminder.ID)
	if err != nil {
		t.Fatalf("ListAuditEvents() error = %v", err)
	}
	var requeueEvent *AuditEvent
	for index := range events {
		if events[index].Action == AuditActionRunRequeued {
			requeueEvent = &events[index]
		}
	}
	if requeueEvent == nil || requeueEvent.Actor.Kind != ActorKindSystem {
		t.Fatalf("run.requeued event = %v", requeueEvent)
	}

	reclaimed := mustClaim(t, service, secondRunner)
	if reclaimed.ID != claimed.ID || reclaimed.RunnerID == nil || *reclaimed.RunnerID != secondRunner.ID {
		t.Fatalf("reclaimed run: %#v", reclaimed)
	}
}

func TestExpireStaleRuns(t *testing.T) {
	t.Parallel()

	service, repository, _ := executionService(t)
	owner, runner, _ := executionActors()
	project := mustCreateProject(t, service, owner, "customer-api")
	policy := mustCreatePolicy(t, service, owner, project.ID, nil)
	mustCreateExecutableReminder(t, service, owner, &policy.ID)
	mustRegisterRunner(t, service, runner, []string{project.ID}, []string{"codex"})
	stale := mustMaterializeOne(t, service, executionNow)

	// A run past its 24h request window expires.
	expired, err := service.ExpireStaleRuns(context.Background(), executionNow.Add(25*time.Hour))
	if err != nil {
		t.Fatalf("ExpireStaleRuns() error = %v", err)
	}
	if len(expired) != 1 || expired[0].ID != stale.ID || expired[0].Status != AgentRunStatusExpired {
		t.Fatalf("expired runs = %#v", expired)
	}
	if expired[0].FinishedAt == nil {
		t.Fatal("expired run must carry a finish time")
	}

	// A run whose occurrence vanished in a reschedule expires.
	secondReminder := mustCreateExecutableReminder(t, service, owner, &policy.ID)
	stranded := mustMaterializeOne(t, service, executionNow)
	if _, err := service.UpdateReminder(context.Background(), owner, secondReminder.ID, UpdateReminderInput{
		Schedule:         pointer(&Schedule{LocalDate: "2026-08-21", LocalTime: "12:30", TimeZone: "UTC", Mode: TimeZoneModeFixed}),
		ExpectedRevision: secondReminder.Revision,
		ClientRequestID:  requestID(),
	}); err != nil {
		t.Fatalf("UpdateReminder(reschedule) error = %v", err)
	}
	if _, err := repository.GetOccurrence(context.Background(), *stranded.OccurrenceID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("stranded occurrence error = %v, want ErrNotFound", err)
	}
	expired, err = service.ExpireStaleRuns(context.Background(), executionNow)
	if err != nil {
		t.Fatalf("ExpireStaleRuns() error = %v", err)
	}
	if len(expired) != 1 || expired[0].ID != stranded.ID {
		t.Fatalf("reschedule expiry = %#v, want the stranded run", expired)
	}

	// A run whose policy got disabled expires; claimed runs are untouched.
	mustCreateExecutableReminder(t, service, owner, &policy.ID)
	claimedRun := mustMaterializeOne(t, service, executionNow)
	claimed := mustClaim(t, service, runner)
	if claimed.ID != claimedRun.ID {
		t.Fatalf("claimed %q, want %q", claimed.ID, claimedRun.ID)
	}
	fourthReminder := mustCreateExecutableReminder(t, service, owner, &policy.ID)
	disabledRun := mustMaterializeOne(t, service, executionNow)
	currentPolicy, err := service.GetPolicy(context.Background(), policy.ID)
	if err != nil {
		t.Fatalf("GetPolicy() error = %v", err)
	}
	if _, err := service.UpdatePolicy(context.Background(), owner, policy.ID, UpdatePolicyInput{
		Enabled:          pointer(false),
		ExpectedRevision: currentPolicy.Revision,
		ClientRequestID:  requestID(),
	}); err != nil {
		t.Fatalf("UpdatePolicy(disable) error = %v", err)
	}
	expired, err = service.ExpireStaleRuns(context.Background(), executionNow)
	if err != nil {
		t.Fatalf("ExpireStaleRuns() error = %v", err)
	}
	if len(expired) != 1 || expired[0].ID != disabledRun.ID {
		t.Fatalf("disabled-policy expiry = %#v, want the unclaimed run", expired)
	}
	survivor, err := service.GetAgentRun(context.Background(), claimedRun.ID)
	if err != nil {
		t.Fatalf("GetAgentRun() error = %v", err)
	}
	if survivor.Status != AgentRunStatusClaimed {
		t.Fatalf("claimed run must survive the sweep, status = %q", survivor.Status)
	}

	events, err := repository.ListAuditEvents(context.Background(), fourthReminder.ID)
	if err != nil {
		t.Fatalf("ListAuditEvents() error = %v", err)
	}
	actions := eventActions(events)
	if !containsAction(actions, AuditActionRunExpired) || !containsAction(actions, AuditActionRunEligible) {
		t.Fatalf("fourth reminder audit actions = %v", actions)
	}
}

func TestRedactSummary(t *testing.T) {
	t.Parallel()

	input := strings.Join([]string{
		"all checks passed",
		"api_key=sk-12345",
		"Token: ghp_999",
		"PASSWORD = hunter2",
		"the secret sauce stayed", // no assignment: kept
		"deploy finished",
	}, "\n")
	redacted := redactSummary(input)
	if strings.Contains(redacted, "sk-12345") || strings.Contains(redacted, "ghp_999") || strings.Contains(redacted, "hunter2") {
		t.Fatalf("redaction leaked secrets: %q", redacted)
	}
	if !strings.Contains(redacted, "all checks passed") || !strings.Contains(redacted, "deploy finished") {
		t.Fatalf("redaction dropped clean lines: %q", redacted)
	}
	if !strings.Contains(redacted, "the secret sauce stayed") {
		t.Fatalf("redaction dropped a clean mention: %q", redacted)
	}

	oversized := strings.Repeat("x", 2500)
	if got := redactSummary(oversized); len([]rune(got)) != 2000 {
		t.Fatalf("redaction length = %d, want 2000", len([]rune(got)))
	}
}

func TestRunnerKindRejectsReminderMutations(t *testing.T) {
	t.Parallel()

	service, _, _ := executionService(t)
	owner, runner, _ := executionActors()
	reminder := mustCreateExecutableReminder(t, service, owner, nil)
	occurrences, err := service.ListOccurrences(context.Background(), reminder.ID, OccurrenceListOptions{})
	if err != nil || len(occurrences) == 0 {
		t.Fatalf("ListOccurrences() = %#v, %v", occurrences, err)
	}

	_, err = service.CreateReminder(context.Background(), runner, CreateReminderInput{
		Title:           "Forged by a runner",
		ClientRequestID: requestID(),
	})
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("runner CreateReminder() error = %v, want ErrForbidden", err)
	}
	title := "Forged"
	_, err = service.UpdateReminder(context.Background(), runner, reminder.ID, UpdateReminderInput{
		Title:            &title,
		ExpectedRevision: reminder.Revision,
		ClientRequestID:  requestID(),
	})
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("runner UpdateReminder() error = %v, want ErrForbidden", err)
	}
	_, err = service.AddComment(context.Background(), runner, reminder.ID, AddCommentInput{
		Body:            "forged",
		ClientRequestID: requestID(),
	})
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("runner AddComment() error = %v, want ErrForbidden", err)
	}
	_, err = service.CompleteOccurrence(context.Background(), runner, occurrences[0].ID, CompleteOccurrenceInput{
		ExpectedRevision: occurrences[0].Revision,
		ClientRequestID:  requestID(),
	})
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("runner CompleteOccurrence() error = %v, want ErrForbidden", err)
	}
	_, err = service.SnoozeOccurrence(context.Background(), runner, occurrences[0].ID, SnoozeOccurrenceInput{
		Until:            executionNow.Add(time.Hour),
		ExpectedRevision: occurrences[0].Revision,
		ClientRequestID:  requestID(),
	})
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("runner SnoozeOccurrence() error = %v, want ErrForbidden", err)
	}
}

func TestListAgentRunsFilters(t *testing.T) {
	t.Parallel()

	service, _, _ := executionService(t)
	owner, runner, _ := executionActors()
	project := mustCreateProject(t, service, owner, "customer-api")
	policy := mustCreatePolicy(t, service, owner, project.ID, nil)
	first := mustCreateExecutableReminder(t, service, owner, &policy.ID)
	second := mustCreateExecutableReminder(t, service, owner, nil)
	mustRegisterRunner(t, service, runner, []string{project.ID}, []string{"codex"})
	mustMaterializeOne(t, service, executionNow)
	claimed := mustClaim(t, service, runner)

	byReminder, err := service.ListAgentRuns(context.Background(), AgentRunListFilter{ReminderID: first.ID})
	if err != nil || len(byReminder) != 1 {
		t.Fatalf("ListAgentRuns(reminder) = %#v, %v", byReminder, err)
	}
	byReminder, err = service.ListAgentRuns(context.Background(), AgentRunListFilter{ReminderID: second.ID})
	if err != nil || len(byReminder) != 0 {
		t.Fatalf("ListAgentRuns(other reminder) = %#v, %v", byReminder, err)
	}
	claimedStatus := AgentRunStatusClaimed
	byStatus, err := service.ListAgentRuns(context.Background(), AgentRunListFilter{Status: &claimedStatus})
	if err != nil || len(byStatus) != 1 {
		t.Fatalf("ListAgentRuns(status) = %#v, %v", byStatus, err)
	}
	byRunner, err := service.ListAgentRuns(context.Background(), AgentRunListFilter{RunnerID: runner.ID})
	if err != nil || len(byRunner) != 1 || byRunner[0].ID != claimed.ID {
		t.Fatalf("ListAgentRuns(runner) = %#v, %v", byRunner, err)
	}
}

func containsAction(actions []AuditAction, target AuditAction) bool {
	for _, action := range actions {
		if action == target {
			return true
		}
	}
	return false
}

func eventActions(events []AuditEvent) []AuditAction {
	actions := make([]AuditAction, 0, len(events))
	for _, event := range events {
		actions = append(actions, event.Action)
	}
	return actions
}

// assertChainLinked verifies the global audit chain: every event links to the
// hash of its predecessor and is sealed.
func assertChainLinked(t *testing.T, service *Service) {
	t.Helper()
	changes, err := service.ListChanges(context.Background(), 0, 500)
	if err != nil {
		t.Fatalf("ListChanges() error = %v", err)
	}
	previous := ""
	for _, change := range changes {
		event := change.Event
		if event.PreviousHash != previous {
			t.Fatalf("event %s has previous hash %q, want %q", event.ID, event.PreviousHash, previous)
		}
		if event.Hash == "" || event.Signature == "" {
			t.Fatalf("event %s is not sealed", event.ID)
		}
		previous = event.Hash
	}
}
