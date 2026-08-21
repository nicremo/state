package store

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/nicremo/state/internal/state"
	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase"
)

var (
	executionNow    = time.Date(2026, time.August, 21, 12, 0, 0, 0, time.UTC)
	executorOwner   = state.Actor{ID: "01989ec9-91ad-7aa9-a48d-a31e76535cd7", Kind: state.ActorKindOwner, DisplayName: "Fabian"}
	executorRunner  = state.Actor{ID: "01989ec9-91ad-7ba9-b499-5659888080b9", Kind: state.ActorKindRunner, DisplayName: "Mac mini"}
	executorRunnerB = state.Actor{ID: "01989ec9-91ad-7cf9-8709-a6caec3d8f13", Kind: state.ActorKindRunner, DisplayName: "Studio"}
)

func newExecutionService(t *testing.T, app *pocketbase.PocketBase) (*state.Service, *PocketBaseRepository) {
	t.Helper()
	seedActorStubs(t, app)
	repository, err := NewPocketBaseRepository(app, deterministicSigningKey())
	if err != nil {
		t.Fatalf("NewPocketBaseRepository() error = %v", err)
	}
	service := state.NewService(repository, state.WithClock(func() time.Time { return executionNow }))
	return service, repository
}

// seedActorStubs provides the state_actors table that internal/auth owns in
// production. The store suite boots without the auth manager, so the runner
// foreign key needs a faithful local stub plus the referenced actor rows.
func seedActorStubs(t *testing.T, app *pocketbase.PocketBase) {
	t.Helper()
	if _, err := app.DB().NewQuery(`CREATE TABLE IF NOT EXISTS state_actors (
		id TEXT PRIMARY KEY,
		kind TEXT NOT NULL,
		display_name TEXT NOT NULL DEFAULT '',
		harness TEXT NOT NULL DEFAULT '',
		device_name TEXT NOT NULL DEFAULT '',
		created_at TEXT NOT NULL,
		revoked_at TEXT
	) STRICT`).Execute(); err != nil {
		t.Fatalf("create state_actors stub error = %v", err)
	}
	for _, actor := range []state.Actor{executorOwner, executorRunner, executorRunnerB} {
		if _, err := app.DB().NewQuery(`
			INSERT OR IGNORE INTO state_actors (id, kind, display_name, created_at)
			VALUES ({:id}, {:kind}, {:display_name}, {:created_at})
		`).Bind(dbx.Params{
			"id":           actor.ID,
			"kind":         string(actor.Kind),
			"display_name": actor.DisplayName,
			"created_at":   formatTime(executionNow),
		}).Execute(); err != nil {
			t.Fatalf("seed actor %s error = %v", actor.ID, err)
		}
	}
}

func execRequestID() string {
	return uuid.NewString()
}

func mustStoreProject(t *testing.T, service *state.Service, name string) state.Project {
	t.Helper()
	project, err := service.CreateProject(context.Background(), executorOwner, state.CreateProjectInput{
		Name:            name,
		Description:     "Customer facing API",
		ClientRequestID: execRequestID(),
	})
	if err != nil {
		t.Fatalf("CreateProject(%q) error = %v", name, err)
	}
	return project
}

func mustStorePolicy(t *testing.T, service *state.Service, projectID string) state.ExecutionPolicy {
	t.Helper()
	policy, err := service.CreatePolicy(context.Background(), executorOwner, state.CreatePolicyInput{
		Name:                        "nightly-review",
		ProjectID:                   projectID,
		Adapter:                     "codex",
		Mode:                        state.ExecutionModeSupervised,
		AllowedCapabilities:         []string{state.CapabilityReadRepository, state.CapabilityRunTests},
		MarkOccurrenceDoneOnSuccess: true,
		NotifyOnCompletion:          true,
		NotifyOnFailure:             true,
		TimeoutMinutes:              30,
		ClientRequestID:             execRequestID(),
	})
	if err != nil {
		t.Fatalf("CreatePolicy() error = %v", err)
	}
	return policy
}

func mustStoreRunner(t *testing.T, service *state.Service, actor state.Actor, projects []string, adapters []string) state.Runner {
	t.Helper()
	runner, err := service.RegisterRunner(context.Background(), actor, state.RegisterRunnerInput{
		DisplayName:     actor.DisplayName,
		Projects:        projects,
		Adapters:        adapters,
		ClientRequestID: execRequestID(),
	})
	if err != nil {
		t.Fatalf("RegisterRunner() error = %v", err)
	}
	return runner
}

func mustStoreReminder(t *testing.T, service *state.Service, policyID *string) state.Reminder {
	t.Helper()
	reminder, err := service.CreateReminder(context.Background(), executorOwner, state.CreateReminderInput{
		Title:             "Review the nightly metrics",
		Description:       "All checks must pass",
		ExecutionPolicyID: policyID,
		ClientRequestID:   execRequestID(),
		Schedule: &state.Schedule{
			LocalDate: "2026-08-21",
			LocalTime: "10:00",
			TimeZone:  "UTC",
			Mode:      state.TimeZoneModeFixed,
		},
	})
	if err != nil {
		t.Fatalf("CreateReminder() error = %v", err)
	}
	return reminder
}

func mustStoreMaterializedRun(t *testing.T, service *state.Service) state.AgentRun {
	t.Helper()
	created, err := service.MaterializeEligibleRuns(context.Background(), executionNow)
	if err != nil {
		t.Fatalf("MaterializeEligibleRuns() error = %v", err)
	}
	if len(created) != 1 {
		t.Fatalf("MaterializeEligibleRuns() created %d runs, want 1", len(created))
	}
	return created[0]
}

func mustStoreClaim(t *testing.T, service *state.Service, runner state.Actor) state.AgentRun {
	t.Helper()
	run, err := service.ClaimAgentRun(context.Background(), runner, state.ClaimRunInput{})
	if err != nil {
		t.Fatalf("ClaimAgentRun() error = %v", err)
	}
	return run
}

func TestExecutionLifecycleEndToEnd(t *testing.T) {
	t.Parallel()

	app := bootstrappedApp(t, t.TempDir())
	service, repository := newExecutionService(t, app)
	project := mustStoreProject(t, service, "customer-api")
	policy := mustStorePolicy(t, service, project.ID)
	reminder := mustStoreReminder(t, service, &policy.ID)
	mustStoreRunner(t, service, executorRunner, []string{project.ID}, []string{"codex"})

	var notifications []state.AgentRun
	serviceWithNotifier := state.NewService(repository,
		state.WithClock(func() time.Time { return executionNow }),
		state.WithRunNotifier(func(_ context.Context, run state.AgentRun, reminderTitle string) error {
			if reminderTitle != reminder.Title {
				t.Errorf("notifier title = %q, want %q", reminderTitle, reminder.Title)
			}
			notifications = append(notifications, run)
			return nil
		}),
	)

	// A repeated scheduler cycle never launches the same occurrence twice.
	run := mustStoreMaterializedRun(t, service)
	again, err := service.MaterializeEligibleRuns(context.Background(), executionNow)
	if err != nil {
		t.Fatalf("second MaterializeEligibleRuns() error = %v", err)
	}
	if len(again) != 0 {
		t.Fatalf("repeated cycle created %d runs, want 0", len(again))
	}
	if run.Status != state.AgentRunStatusEligible || run.OccurrenceID == nil || run.TaskContract.ComputeHash() != run.TaskContract.ContractHash {
		t.Fatalf("unexpected materialized run: %#v", run)
	}

	claimed := mustStoreClaim(t, service, executorRunner)
	if claimed.Status != state.AgentRunStatusClaimed || claimed.RunnerID == nil || *claimed.RunnerID != executorRunner.ID {
		t.Fatalf("unexpected claimed run: %#v", claimed)
	}

	started, err := service.ReportAgentRunEvent(context.Background(), executorRunner, state.ReportRunEventInput{
		RunID:            claimed.ID,
		Event:            state.RunEventStarted,
		ExpectedRevision: claimed.Revision,
	})
	if err != nil {
		t.Fatalf("ReportAgentRunEvent(started) error = %v", err)
	}
	progressed, err := service.ReportAgentRunEvent(context.Background(), executorRunner, state.ReportRunEventInput{
		RunID:            started.ID,
		Event:            state.RunEventProgress,
		Detail:           "half the checks passed",
		ExpectedRevision: started.Revision,
	})
	if err != nil {
		t.Fatalf("ReportAgentRunEvent(progress) error = %v", err)
	}

	eventsBeforeHeartbeat, err := repository.ListAuditEvents(context.Background(), reminder.ID)
	if err != nil {
		t.Fatalf("ListAuditEvents() error = %v", err)
	}
	heartbeat, err := service.ReportAgentRunEvent(context.Background(), executorRunner, state.ReportRunEventInput{
		RunID:            progressed.ID,
		Event:            state.RunEventHeartbeat,
		ExpectedRevision: progressed.Revision,
	})
	if err != nil {
		t.Fatalf("ReportAgentRunEvent(heartbeat) error = %v", err)
	}
	if heartbeat.Revision != progressed.Revision+1 {
		t.Fatalf("heartbeat revision = %d, want %d", heartbeat.Revision, progressed.Revision+1)
	}
	eventsAfterHeartbeat, err := repository.ListAuditEvents(context.Background(), reminder.ID)
	if err != nil {
		t.Fatalf("ListAuditEvents() error = %v", err)
	}
	if len(eventsAfterHeartbeat) != len(eventsBeforeHeartbeat) {
		t.Fatalf("heartbeat grew the audit chain: %d -> %d", len(eventsBeforeHeartbeat), len(eventsAfterHeartbeat))
	}

	completed, err := serviceWithNotifier.CompleteAgentRun(context.Background(), executorRunner, state.CompleteRunInput{
		RunID:            heartbeat.ID,
		Outcome:          state.AgentRunStatusSucceeded,
		ResultSummary:    "all checks passed\napi_token=sk-9",
		ExitCode:         0,
		ExpectedRevision: heartbeat.Revision,
		MutationMetadata: state.MutationMetadata{ClientRequestID: execRequestID()},
	})
	if err != nil {
		t.Fatalf("CompleteAgentRun() error = %v", err)
	}
	if completed.Status != state.AgentRunStatusSucceeded || completed.ResultSummary != "all checks passed" {
		t.Fatalf("unexpected completed run: %#v", completed)
	}
	if len(notifications) != 1 || notifications[0].ID != completed.ID {
		t.Fatalf("run completion notifications = %#v", notifications)
	}
	occurrence, err := repository.GetOccurrence(context.Background(), *run.OccurrenceID)
	if err != nil {
		t.Fatalf("GetOccurrence() error = %v", err)
	}
	if occurrence.Status != state.OccurrenceStatusCompleted {
		t.Fatalf("occurrence must be completed: %#v", occurrence)
	}
	if err := repository.VerifyAuditChain(context.Background()); err != nil {
		t.Fatalf("VerifyAuditChain() error = %v", err)
	}

	// The full lifecycle appears in the run's reminder timeline.
	events, err := repository.ListAuditEvents(context.Background(), reminder.ID)
	if err != nil {
		t.Fatalf("ListAuditEvents() error = %v", err)
	}
	wantActions := []state.AuditAction{
		state.AuditActionCreated,
		state.AuditActionRunEligible,
		state.AuditActionRunClaimed,
		state.AuditActionRunStarted,
		state.AuditActionRunProgress,
		state.AuditActionRunSucceeded,
		state.AuditActionOccurrenceDone,
	}
	if len(events) != len(wantActions) {
		t.Fatalf("audit actions = %#v", events)
	}
	for index, want := range wantActions {
		if events[index].Action != want {
			t.Fatalf("audit action %d = %q, want %q", index, events[index].Action, want)
		}
		if index > 0 && events[index].CorrelationID != run.ID {
			t.Fatalf("run event %d correlation = %q, want %q", index, events[index].CorrelationID, run.ID)
		}
	}

	// The adapter and project text ride the audit search index.
	found, err := service.SearchReminders(context.Background(), "codex", 10)
	if err != nil {
		t.Fatalf("SearchReminders() error = %v", err)
	}
	if len(found) != 1 || found[0].ID != reminder.ID {
		t.Fatalf("SearchReminders(codex) = %#v", found)
	}
}

func TestStoreClaimIsAtomicAcrossRunners(t *testing.T) {
	t.Parallel()

	app := bootstrappedApp(t, t.TempDir())
	service, repository := newExecutionService(t, app)
	project := mustStoreProject(t, service, "customer-api")
	policy := mustStorePolicy(t, service, project.ID)
	mustStoreReminder(t, service, &policy.ID)
	mustStoreRunner(t, service, executorRunner, []string{project.ID}, []string{"codex"})
	mustStoreRunner(t, service, executorRunnerB, []string{project.ID}, []string{"codex"})
	mustStoreMaterializedRun(t, service)

	type claimResult struct {
		run state.AgentRun
		err error
	}
	results := make(chan claimResult, 2)
	var wait sync.WaitGroup
	for _, actor := range []state.Actor{executorRunner, executorRunnerB} {
		wait.Add(1)
		go func(actor state.Actor) {
			defer wait.Done()
			run, err := service.ClaimAgentRun(context.Background(), actor, state.ClaimRunInput{})
			results <- claimResult{run: run, err: err}
		}(actor)
	}
	wait.Wait()
	close(results)

	wins := 0
	for result := range results {
		if result.err == nil {
			wins++
			continue
		}
		if !errors.Is(result.err, state.ErrNotClaimable) {
			t.Fatalf("losing claim error = %v, want ErrNotClaimable", result.err)
		}
	}
	if wins != 1 {
		t.Fatalf("claim wins = %d, want exactly 1", wins)
	}
	runs, err := service.ListAgentRuns(context.Background(), state.AgentRunListFilter{})
	if err != nil {
		t.Fatalf("ListAgentRuns() error = %v", err)
	}
	if len(runs) != 1 || runs[0].Status != state.AgentRunStatusClaimed || runs[0].RunnerID == nil {
		t.Fatalf("runs after claim race = %#v", runs)
	}
	if err := repository.VerifyAuditChain(context.Background()); err != nil {
		t.Fatalf("VerifyAuditChain() error = %v", err)
	}
}

func TestStoreManualRunCreationIsIdempotent(t *testing.T) {
	t.Parallel()

	app := bootstrappedApp(t, t.TempDir())
	service, repository := newExecutionService(t, app)
	project := mustStoreProject(t, service, "customer-api")
	policy := mustStorePolicy(t, service, project.ID)
	reminder := mustStoreReminder(t, service, &policy.ID)

	input := state.CreateManualRunInput{
		ReminderID:       reminder.ID,
		PolicyID:         policy.ID,
		MutationMetadata: state.MutationMetadata{ClientRequestID: execRequestID()},
	}
	run, err := service.CreateManualRun(context.Background(), executorOwner, input)
	if err != nil {
		t.Fatalf("CreateManualRun() error = %v", err)
	}
	replayed, err := service.CreateManualRun(context.Background(), executorOwner, input)
	if err != nil {
		t.Fatalf("replayed CreateManualRun() error = %v", err)
	}
	if replayed.ID != run.ID {
		t.Fatalf("replayed run ID = %q, want %q", replayed.ID, run.ID)
	}
	runs, err := service.ListAgentRuns(context.Background(), state.AgentRunListFilter{ReminderID: reminder.ID})
	if err != nil {
		t.Fatalf("ListAgentRuns() error = %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("run count after replay = %d, want 1", len(runs))
	}
	if err := repository.VerifyAuditChain(context.Background()); err != nil {
		t.Fatalf("VerifyAuditChain() error = %v", err)
	}
}

func TestStoreEvidenceMismatchForcesFailure(t *testing.T) {
	t.Parallel()

	app := bootstrappedApp(t, t.TempDir())
	service, repository := newExecutionService(t, app)
	project := mustStoreProject(t, service, "customer-api")
	policy := mustStorePolicy(t, service, project.ID)
	mustStoreReminder(t, service, &policy.ID)
	mustStoreRunner(t, service, executorRunner, []string{project.ID}, []string{"codex"})
	run := mustStoreMaterializedRun(t, service)
	claimed := mustStoreClaim(t, service, executorRunner)

	completed, err := service.CompleteAgentRun(context.Background(), executorRunner, state.CompleteRunInput{
		RunID:            claimed.ID,
		Outcome:          state.AgentRunStatusSucceeded,
		ExitCode:         1,
		ExpectedRevision: claimed.Revision,
		MutationMetadata: state.MutationMetadata{ClientRequestID: execRequestID()},
	})
	if err != nil {
		t.Fatalf("CompleteAgentRun() error = %v", err)
	}
	if completed.Status != state.AgentRunStatusFailed || completed.FailureCode != state.RunFailureEvidenceMismatch {
		t.Fatalf("evidence mismatch run = %#v", completed)
	}
	occurrence, err := repository.GetOccurrence(context.Background(), *run.OccurrenceID)
	if err != nil {
		t.Fatalf("GetOccurrence() error = %v", err)
	}
	if occurrence.Status != state.OccurrenceStatusPending {
		t.Fatalf("evidence mismatch must not complete the occurrence: %#v", occurrence)
	}
	if err := repository.VerifyAuditChain(context.Background()); err != nil {
		t.Fatalf("VerifyAuditChain() error = %v", err)
	}
}

func TestStoreApprovalAndCancelFlow(t *testing.T) {
	t.Parallel()

	app := bootstrappedApp(t, t.TempDir())
	service, repository := newExecutionService(t, app)
	project := mustStoreProject(t, service, "customer-api")
	policy, err := service.CreatePolicy(context.Background(), executorOwner, state.CreatePolicyInput{
		Name:                "nightly-review",
		ProjectID:           project.ID,
		Adapter:             "codex",
		Mode:                state.ExecutionModeSupervised,
		AllowedCapabilities: []string{state.CapabilityReadRepository},
		TimeoutMinutes:      30,
		NotifyOnFailure:     true,
		ClientRequestID:     execRequestID(),
	})
	if err != nil {
		t.Fatalf("CreatePolicy() error = %v", err)
	}
	mustStoreReminder(t, service, &policy.ID)
	mustStoreRunner(t, service, executorRunner, []string{project.ID}, []string{"codex"})
	mustStoreMaterializedRun(t, service)
	claimed := mustStoreClaim(t, service, executorRunner)
	running, err := service.ReportAgentRunEvent(context.Background(), executorRunner, state.ReportRunEventInput{
		RunID:            claimed.ID,
		Event:            state.RunEventStarted,
		ExpectedRevision: claimed.Revision,
	})
	if err != nil {
		t.Fatalf("ReportAgentRunEvent(started) error = %v", err)
	}

	waiting, err := service.RequestAgentApproval(context.Background(), executorRunner, state.RequestApprovalInput{
		RunID:            running.ID,
		Capability:       state.CapabilityDeploy,
		Reason:           "deploy to staging",
		ExpectedRevision: running.Revision,
	})
	if err != nil {
		t.Fatalf("RequestAgentApproval() error = %v", err)
	}
	if waiting.Status != state.AgentRunStatusNeedsApproval {
		t.Fatalf("run must wait for approval: %#v", waiting)
	}

	approved, err := service.ApproveAgentRun(context.Background(), executorOwner, state.ApproveRunInput{
		RunID:            waiting.ID,
		Approved:         true,
		ExpectedRevision: waiting.Revision,
		MutationMetadata: state.MutationMetadata{ClientRequestID: execRequestID()},
	})
	if err != nil {
		t.Fatalf("ApproveAgentRun() error = %v", err)
	}
	if approved.Status != state.AgentRunStatusClaimed || approved.LeaseExpiresAt == nil {
		t.Fatalf("approved run = %#v", approved)
	}

	cancelled, err := service.CancelAgentRun(context.Background(), executorOwner, state.CancelRunInput{
		RunID:            approved.ID,
		ExpectedRevision: approved.Revision,
		MutationMetadata: state.MutationMetadata{ClientRequestID: execRequestID()},
	})
	if err != nil {
		t.Fatalf("CancelAgentRun() error = %v", err)
	}
	if cancelled.Status != state.AgentRunStatusCancelled || cancelled.FinishedAt == nil {
		t.Fatalf("cancelled run = %#v", cancelled)
	}

	// A terminal run rejects further transitions.
	_, err = service.ReportAgentRunEvent(context.Background(), executorRunner, state.ReportRunEventInput{
		RunID:            cancelled.ID,
		Event:            state.RunEventProgress,
		ExpectedRevision: cancelled.Revision,
	})
	if !errors.Is(err, state.ErrRunStateConflict) {
		t.Fatalf("report on cancelled run error = %v, want ErrRunStateConflict", err)
	}
	if err := repository.VerifyAuditChain(context.Background()); err != nil {
		t.Fatalf("VerifyAuditChain() error = %v", err)
	}
}

func TestStoreLeaseRequeueAndExpirySweeps(t *testing.T) {
	t.Parallel()

	app := bootstrappedApp(t, t.TempDir())
	service, repository := newExecutionService(t, app)
	project := mustStoreProject(t, service, "customer-api")
	policy := mustStorePolicy(t, service, project.ID)
	reminder := mustStoreReminder(t, service, &policy.ID)
	mustStoreRunner(t, service, executorRunner, []string{project.ID}, []string{"codex"})
	mustStoreRunner(t, service, executorRunnerB, []string{project.ID}, []string{"codex"})
	run := mustStoreMaterializedRun(t, service)
	claimed := mustStoreClaim(t, service, executorRunner)

	requeued, err := service.RequeueExpiredLeases(context.Background(), executionNow.Add(61*time.Minute))
	if err != nil {
		t.Fatalf("RequeueExpiredLeases() error = %v", err)
	}
	if len(requeued) != 1 || requeued[0].ID != run.ID || requeued[0].Status != state.AgentRunStatusEligible {
		t.Fatalf("requeued runs = %#v", requeued)
	}
	if requeued[0].Revision != claimed.Revision+1 {
		t.Fatalf("requeued revision = %d, want %d", requeued[0].Revision, claimed.Revision+1)
	}
	reclaimed := mustStoreClaim(t, service, executorRunnerB)
	if reclaimed.ID != run.ID || *reclaimed.RunnerID != executorRunnerB.ID {
		t.Fatalf("reclaimed run = %#v", reclaimed)
	}

	// A claimed run survives expiry; after cancellation the sweep finds
	// nothing terminal.
	expired, err := service.ExpireStaleRuns(context.Background(), executionNow.Add(25*time.Hour))
	if err != nil {
		t.Fatalf("ExpireStaleRuns() error = %v", err)
	}
	if len(expired) != 0 {
		t.Fatalf("claimed run must not expire: %#v", expired)
	}

	// An unclaimed run expires after 24h.
	mustStoreReminder(t, service, &policy.ID)
	staleRun := mustStoreMaterializedRun(t, service)
	expired, err = service.ExpireStaleRuns(context.Background(), executionNow.Add(25*time.Hour))
	if err != nil {
		t.Fatalf("ExpireStaleRuns() error = %v", err)
	}
	if len(expired) != 1 || expired[0].ID != staleRun.ID || expired[0].Status != state.AgentRunStatusExpired {
		t.Fatalf("expired runs = %#v", expired)
	}

	events, err := repository.ListAuditEvents(context.Background(), reminder.ID)
	if err != nil {
		t.Fatalf("ListAuditEvents() error = %v", err)
	}
	var sawRequeue bool
	for _, event := range events {
		if event.Action == state.AuditActionRunRequeued && event.Actor.Kind == state.ActorKindSystem {
			sawRequeue = true
		}
	}
	if !sawRequeue {
		t.Fatal("run.requeued event missing")
	}
	if err := repository.VerifyAuditChain(context.Background()); err != nil {
		t.Fatalf("VerifyAuditChain() error = %v", err)
	}
}

func TestStoreRunnerRegistrationAndSeenTracking(t *testing.T) {
	t.Parallel()

	app := bootstrappedApp(t, t.TempDir())
	service, repository := newExecutionService(t, app)
	project := mustStoreProject(t, service, "customer-api")

	registered := mustStoreRunner(t, service, executorRunner, []string{project.ID}, []string{"codex"})
	if registered.Revision != 1 || registered.LastSeenAt != executionNow {
		t.Fatalf("unexpected runner: %#v", registered)
	}

	// Claiming touches last-seen without bumping the revision.
	_, err := service.ClaimAgentRun(context.Background(), executorRunner, state.ClaimRunInput{})
	if !errors.Is(err, state.ErrNotClaimable) {
		t.Fatalf("empty claim error = %v, want ErrNotClaimable", err)
	}
	profile, err := service.GetRunner(context.Background(), executorRunner.ID)
	if err != nil {
		t.Fatalf("GetRunner() error = %v", err)
	}
	if profile.Revision != 1 || !profile.LastSeenAt.Equal(executionNow) {
		t.Fatalf("runner profile after touch = %#v", profile)
	}

	updated, err := service.UpdateRunner(context.Background(), executorOwner, executorRunner.ID, state.UpdateRunnerInput{
		Adapters:         &[]string{"codex", "claude-code"},
		ExpectedRevision: 1,
		ClientRequestID:  execRequestID(),
	})
	if err != nil {
		t.Fatalf("UpdateRunner() error = %v", err)
	}
	if updated.Revision != 2 || len(updated.Adapters) != 2 {
		t.Fatalf("updated runner = %#v", updated)
	}

	// Runner and project events carry no reminder and flow through the global
	// change feed with an empty reminder ID.
	changes, err := service.ListChanges(context.Background(), 0, 500)
	if err != nil {
		t.Fatalf("ListChanges() error = %v", err)
	}
	var sawRunnerEvent bool
	for _, change := range changes {
		if change.Event.Action == state.AuditActionRunnerRegistered {
			sawRunnerEvent = true
			if change.Event.ReminderID != "" {
				t.Fatalf("runner event reminder ID = %q, want empty", change.Event.ReminderID)
			}
		}
	}
	if !sawRunnerEvent {
		t.Fatal("runner.registered event missing from the change feed")
	}
	if err := repository.VerifyAuditChain(context.Background()); err != nil {
		t.Fatalf("VerifyAuditChain() error = %v", err)
	}
}

func TestExecutionTablesSurviveRestart(t *testing.T) {
	t.Parallel()

	dataDirectory := t.TempDir()
	app := bootstrappedApp(t, dataDirectory)
	service, _ := newExecutionService(t, app)
	project := mustStoreProject(t, service, "customer-api")
	policy := mustStorePolicy(t, service, project.ID)
	reminder := mustStoreReminder(t, service, &policy.ID)
	mustStoreRunner(t, service, executorRunner, []string{project.ID}, []string{"codex"})
	run := mustStoreMaterializedRun(t, service)
	claimed := mustStoreClaim(t, service, executorRunner)
	if err := app.ResetBootstrapState(); err != nil {
		t.Fatalf("ResetBootstrapState() error = %v", err)
	}

	restartedApp := bootstrappedApp(t, dataDirectory)
	restartedService, restartedRepository := newExecutionService(t, restartedApp)

	projects, err := restartedService.ListProjects(context.Background())
	if err != nil || len(projects) != 1 || projects[0].Name != "customer-api" {
		t.Fatalf("ListProjects() after restart = %#v, %v", projects, err)
	}
	policies, err := restartedService.ListPolicies(context.Background())
	if err != nil || len(policies) != 1 || policies[0].Name != "nightly-review" {
		t.Fatalf("ListPolicies() after restart = %#v, %v", policies, err)
	}
	runners, err := restartedService.ListRunners(context.Background())
	if err != nil || len(runners) != 1 || runners[0].DisplayName != "Mac mini" {
		t.Fatalf("ListRunners() after restart = %#v, %v", runners, err)
	}
	runs, err := restartedService.ListAgentRuns(context.Background(), state.AgentRunListFilter{ReminderID: reminder.ID})
	if err != nil || len(runs) != 1 {
		t.Fatalf("ListAgentRuns() after restart = %#v, %v", runs, err)
	}
	if runs[0].ID != run.ID || runs[0].Status != state.AgentRunStatusClaimed || runs[0].RunnerID == nil || *runs[0].RunnerID != *claimed.RunnerID {
		t.Fatalf("run after restart = %#v", runs[0])
	}
	if err := restartedRepository.VerifyAuditChain(context.Background()); err != nil {
		t.Fatalf("VerifyAuditChain() after restart error = %v", err)
	}
}
