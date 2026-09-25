package store

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/nicremo/state/internal/state"
)

func TestAgentSessionsPersistWithRoundsAndAudit(t *testing.T) {
	t.Parallel()
	dataDirectory := t.TempDir()
	service, repository := newExecutionService(t, bootstrappedApp(t, dataDirectory))
	ctx := context.Background()
	owner := executorOwner
	runner := executorRunner

	project, err := service.CreateProject(ctx, owner, state.CreateProjectInput{Name: "karla-report", ClientRequestID: uuid.NewString()})
	if err != nil {
		t.Fatal(err)
	}
	policy, err := service.CreatePolicy(ctx, owner, state.CreatePolicyInput{
		Name: "karla-claude", ProjectID: project.ID, Adapter: "claude-code", Mode: state.ExecutionModeSupervised,
		AllowedCapabilities: []string{state.CapabilityReadRepository}, TimeoutMinutes: 30, ClientRequestID: uuid.NewString(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.RegisterRunner(ctx, runner, state.RegisterRunnerInput{DisplayName: "Mac", Projects: []string{project.ID}, Adapters: []string{"claude-code"}, ClientRequestID: uuid.NewString()}); err != nil {
		t.Fatal(err)
	}
	reminder, err := service.CreateReminder(ctx, owner, state.CreateReminderInput{Title: "Karla-Monatsreport", ClientRequestID: uuid.NewString()})
	if err != nil {
		t.Fatal(err)
	}
	startRequest := uuid.NewString()
	started, err := service.StartAgentSession(ctx, owner, state.StartAgentSessionInput{ReminderID: reminder.ID, PolicyID: policy.ID, Instruction: "Los", MutationMetadata: state.MutationMetadata{ClientRequestID: startRequest}})
	if err != nil {
		t.Fatalf("StartAgentSession() error = %v", err)
	}
	replay, err := service.StartAgentSession(ctx, owner, state.StartAgentSessionInput{ReminderID: reminder.ID, PolicyID: policy.ID, Instruction: "Los", MutationMetadata: state.MutationMetadata{ClientRequestID: startRequest}})
	if err != nil || replay.ID != started.ID || len(replay.Turns) != 1 {
		t.Fatalf("replay = %v %d %v", replay.ID, len(replay.Turns), err)
	}

	run, err := service.ClaimAgentRun(ctx, runner, state.ClaimRunInput{})
	if err != nil || run.SessionID != started.ID {
		t.Fatalf("claim = %+v, %v", run, err)
	}
	if _, err := service.SendAgentSessionMessage(ctx, owner, state.SendAgentSessionMessageInput{SessionID: started.ID, Text: "Weiter", MutationMetadata: state.MutationMetadata{ClientRequestID: uuid.NewString()}}); err != state.ErrRunStateConflict {
		t.Fatalf("second round while working = %v", err)
	}
	run, err = service.ReportAgentRunEvent(ctx, runner, state.ReportRunEventInput{RunID: run.ID, Event: state.RunEventStarted, ExpectedRevision: run.Revision})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.CompleteAgentRun(ctx, runner, state.CompleteRunInput{
		RunID: run.ID, Outcome: state.AgentRunStatusSucceeded, ResultSummary: "Fertig", ResultText: "## Fertig\nReport liegt bereit.",
		HarnessSessionID: "e11a322c-c64c-41ef-990a-ee6d22767bc3", ExpectedRevision: run.Revision,
		MutationMetadata: state.MutationMetadata{ClientRequestID: uuid.NewString()},
	}); err != nil {
		t.Fatal(err)
	}
	next, err := service.SendAgentSessionMessage(ctx, owner, state.SendAgentSessionMessageInput{SessionID: started.ID, Text: "Schick ihn an Karla", MutationMetadata: state.MutationMetadata{ClientRequestID: uuid.NewString()}})
	if err != nil || len(next.Turns) != 2 || next.Turns[1].TaskContract.ResumeSessionID != "e11a322c-c64c-41ef-990a-ee6d22767bc3" {
		t.Fatalf("second round = %+v, %v", next.Turns, err)
	}
	if err := repository.VerifyAuditChain(ctx); err != nil {
		t.Fatalf("audit chain: %v", err)
	}

	reopened, err := NewPocketBaseRepository(bootstrappedApp(t, dataDirectory), deterministicSigningKey())
	if err != nil {
		t.Fatal(err)
	}
	restarted := state.NewService(reopened)
	view, err := restarted.GetAgentSession(ctx, started.ID)
	if err != nil || view.Status != state.SessionStatusWorking || len(view.Turns) != 2 || view.Turns[0].ResultText != "## Fertig\nReport liegt bereit." || view.Turns[1].Prompt != "Schick ihn an Karla" {
		t.Fatalf("after restart = %+v, %v", view, err)
	}
	sessions, err := restarted.ListAgentSessions(ctx, 10)
	if err != nil || len(sessions) != 1 {
		t.Fatalf("list = %d, %v", len(sessions), err)
	}
}

// Review: two devices answering at the same moment must not start two
// rounds; the check runs inside the insert transaction.
func TestConcurrentMessagesStartOneRound(t *testing.T) {
	t.Parallel()
	dataDirectory := t.TempDir()
	service, _ := newExecutionService(t, bootstrappedApp(t, dataDirectory))
	ctx := context.Background()
	project, err := service.CreateProject(ctx, executorOwner, state.CreateProjectInput{Name: "karla-report", ClientRequestID: uuid.NewString()})
	if err != nil {
		t.Fatal(err)
	}
	policy, err := service.CreatePolicy(ctx, executorOwner, state.CreatePolicyInput{
		Name: "karla-claude", ProjectID: project.ID, Adapter: "claude-code", Mode: state.ExecutionModeSupervised,
		AllowedCapabilities: []string{state.CapabilityReadRepository}, TimeoutMinutes: 30, ClientRequestID: uuid.NewString(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.RegisterRunner(ctx, executorRunner, state.RegisterRunnerInput{DisplayName: "Mac", Projects: []string{project.ID}, Adapters: []string{"claude-code"}, ClientRequestID: uuid.NewString()}); err != nil {
		t.Fatal(err)
	}
	reminder, err := service.CreateReminder(ctx, executorOwner, state.CreateReminderInput{Title: "Karla", ClientRequestID: uuid.NewString()})
	if err != nil {
		t.Fatal(err)
	}
	session, err := service.StartAgentSession(ctx, executorOwner, state.StartAgentSessionInput{ReminderID: reminder.ID, PolicyID: policy.ID, MutationMetadata: state.MutationMetadata{ClientRequestID: uuid.NewString()}})
	if err != nil {
		t.Fatal(err)
	}
	run, _ := service.ClaimAgentRun(ctx, executorRunner, state.ClaimRunInput{})
	run, _ = service.ReportAgentRunEvent(ctx, executorRunner, state.ReportRunEventInput{RunID: run.ID, Event: state.RunEventStarted, ExpectedRevision: run.Revision})
	if _, err := service.CompleteAgentRun(ctx, executorRunner, state.CompleteRunInput{RunID: run.ID, Outcome: state.AgentRunStatusSucceeded, ResultText: "Fertig", HarnessSessionID: "s-1", ExpectedRevision: run.Revision, MutationMetadata: state.MutationMetadata{ClientRequestID: uuid.NewString()}}); err != nil {
		t.Fatal(err)
	}

	const senders = 8
	results := make(chan error, senders)
	for index := 0; index < senders; index++ {
		go func() {
			_, err := service.SendAgentSessionMessage(ctx, executorOwner, state.SendAgentSessionMessageInput{SessionID: session.ID, Text: "Weiter", MutationMetadata: state.MutationMetadata{ClientRequestID: uuid.NewString()}})
			results <- err
		}()
	}
	succeeded := 0
	for index := 0; index < senders; index++ {
		err := <-results
		switch {
		case err == nil:
			succeeded++
		case errors.Is(err, state.ErrRunStateConflict):
		default:
			t.Errorf("unexpected error: %v", err)
		}
	}
	view, _ := service.GetAgentSession(ctx, session.ID)
	if succeeded != 1 || len(view.Turns) != 2 {
		t.Fatalf("succeeded = %d, rounds = %d, want exactly one new round", succeeded, len(view.Turns))
	}
}
