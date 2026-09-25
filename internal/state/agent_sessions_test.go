package state

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

type sessionFixture struct {
	service  *Service
	owner    Actor
	runner   Actor
	reminder Reminder
	policy   ExecutionPolicy
}

func newSessionFixture(t *testing.T) sessionFixture {
	t.Helper()
	service, _, _ := executionService(t)
	owner, runner, _ := executionActors()
	project := mustCreateProject(t, service, owner, "karla-report")
	policy := mustCreatePolicy(t, service, owner, project.ID, func(input *CreatePolicyInput) {
		input.Name = "karla-claude"
		input.Adapter = "claude-code"
		input.NotifyOnCompletion = false
		input.NotifyOnFailure = false
	})
	mustRegisterRunner(t, service, runner, []string{project.ID}, []string{"claude-code"})
	reminder := mustCreateExecutableReminder(t, service, owner, nil)
	return sessionFixture{service: service, owner: owner, runner: runner, reminder: reminder, policy: policy}
}

func (fixture sessionFixture) start(t *testing.T, instruction string) AgentSessionView {
	t.Helper()
	view, err := fixture.service.StartAgentSession(context.Background(), fixture.owner, StartAgentSessionInput{
		ReminderID:       fixture.reminder.ID,
		PolicyID:         fixture.policy.ID,
		Instruction:      instruction,
		MutationMetadata: MutationMetadata{ClientRequestID: requestID()},
	})
	if err != nil {
		t.Fatalf("StartAgentSession() error = %v", err)
	}
	return view
}

// finishTurn claims the session's open round as the runner and completes it
// with the agent's answer and the CLI session ID.
func (fixture sessionFixture) finishTurn(t *testing.T, outcome AgentRunStatus, text string, harnessID string) AgentRun {
	t.Helper()
	ctx := context.Background()
	run := mustClaim(t, fixture.service, fixture.runner)
	run = mustReportStarted(t, fixture.service, fixture.runner, run)
	exitCode := 0
	if outcome != AgentRunStatusSucceeded {
		exitCode = 1
	}
	completed, err := fixture.service.CompleteAgentRun(ctx, fixture.runner, CompleteRunInput{
		RunID:            run.ID,
		Outcome:          outcome,
		ResultSummary:    text,
		ResultText:       text,
		HarnessSessionID: harnessID,
		ExitCode:         exitCode,
		ExpectedRevision: run.Revision,
		MutationMetadata: MutationMetadata{ClientRequestID: requestID()},
	})
	if err != nil {
		t.Fatalf("CompleteAgentRun() error = %v", err)
	}
	return completed
}

func (fixture sessionFixture) send(t *testing.T, sessionID string, text string) (AgentSessionView, error) {
	t.Helper()
	return fixture.service.SendAgentSessionMessage(context.Background(), fixture.owner, SendAgentSessionMessageInput{
		SessionID:        sessionID,
		Text:             text,
		MutationMetadata: MutationMetadata{ClientRequestID: requestID()},
	})
}

func TestStartAgentSessionCreatesTheFirstRound(t *testing.T) {
	t.Parallel()
	fixture := newSessionFixture(t)
	view := fixture.start(t, "Nimm die Zahlen vom September.")

	if view.Title != fixture.reminder.Title || view.Adapter != "claude-code" || view.Status != SessionStatusWorking || view.Closed {
		t.Fatalf("session = %+v", view.AgentSession)
	}
	if len(view.Turns) != 1 {
		t.Fatalf("turns = %d, want 1", len(view.Turns))
	}
	turn := view.Turns[0]
	if turn.SessionID != view.ID || turn.TurnKind != TurnKindMessage || turn.Status != AgentRunStatusEligible {
		t.Fatalf("first round = %+v", turn)
	}
	contract := turn.TaskContract
	for _, wanted := range []string{fixture.reminder.Title, fixture.reminder.Description, "Nimm die Zahlen vom September.", "State session"} {
		if !strings.Contains(contract.Objective, wanted) {
			t.Fatalf("objective lacks %q: %q", wanted, contract.Objective)
		}
	}
	if contract.SessionID != view.ID || contract.TurnKind != string(TurnKindMessage) || contract.ResumeSessionID != "" {
		t.Fatalf("contract session fields = %+v", contract)
	}
	if contract.ContractHash != contract.ComputeHash() {
		t.Fatal("contract hash does not cover the session fields")
	}
	if turn.Prompt != "Nimm die Zahlen vom September." {
		t.Fatalf("prompt = %q, want the owner's instruction", turn.Prompt)
	}
}

func TestOnlyOwnerAndDevicesDriveSessions(t *testing.T) {
	t.Parallel()
	fixture := newSessionFixture(t)
	ctx := context.Background()
	harness := Actor{ID: "01989d9b-b5c4-7aa9-a48d-000000000009", Kind: ActorKindHarness, DisplayName: "Codex", Harness: "codex"}
	for _, actor := range []Actor{harness, fixture.runner} {
		_, err := fixture.service.StartAgentSession(ctx, actor, StartAgentSessionInput{
			ReminderID: fixture.reminder.ID, PolicyID: fixture.policy.ID,
			MutationMetadata: MutationMetadata{ClientRequestID: requestID()},
		})
		if !errors.Is(err, ErrForbidden) {
			t.Fatalf("%s started a session: %v", actor.Kind, err)
		}
	}
	device := Actor{ID: "01989d9b-b5c4-7aa9-a48d-00000000000a", Kind: ActorKindDevice, DisplayName: "iPhone"}
	if _, err := fixture.service.StartAgentSession(ctx, device, StartAgentSessionInput{
		ReminderID: fixture.reminder.ID, PolicyID: fixture.policy.ID,
		MutationMetadata: MutationMetadata{ClientRequestID: requestID()},
	}); err != nil {
		t.Fatalf("device cannot start a session: %v", err)
	}
}

func TestSessionRoundsResumeTheAgentSession(t *testing.T) {
	t.Parallel()
	fixture := newSessionFixture(t)
	view := fixture.start(t, "")

	if _, err := fixture.send(t, view.ID, "Weiter"); !errors.Is(err, ErrRunStateConflict) {
		t.Fatalf("message while working = %v, want conflict", err)
	}
	first := fixture.finishTurn(t, AgentRunStatusSucceeded, "## Erledigt\nReport gebaut. Soll ich ihn verschicken?", "claude-session-1")
	if first.ResultText == "" || first.HarnessSessionID != "claude-session-1" {
		t.Fatalf("first round = %+v", first)
	}

	waiting, err := fixture.service.GetAgentSession(context.Background(), view.ID)
	if err != nil || waiting.Status != SessionStatusWaiting || waiting.HarnessSessionID != "claude-session-1" {
		t.Fatalf("after round = %+v, %v", waiting.AgentSession, err)
	}

	next, err := fixture.send(t, view.ID, "Nein, erst die Zahlen prüfen.")
	if err != nil {
		t.Fatalf("SendAgentSessionMessage() error = %v", err)
	}
	if len(next.Turns) != 2 || next.Status != SessionStatusWorking {
		t.Fatalf("session = %+v, turns %d", next.AgentSession, len(next.Turns))
	}
	second := next.Turns[1]
	if second.Prompt != "Nein, erst die Zahlen prüfen." || second.TaskContract.Objective != "Nein, erst die Zahlen prüfen." || second.TaskContract.ResumeSessionID != "claude-session-1" {
		t.Fatalf("second round = %+v", second.TaskContract)
	}

	if _, err := fixture.send(t, view.ID, strings.Repeat("x", 8001)); !errors.Is(err, ErrInvalidInput) && !errors.Is(err, ErrRunStateConflict) {
		t.Fatalf("oversized message = %v", err)
	}
	fixture.finishTurn(t, AgentRunStatusFailed, "Fehler beim Lesen", "")
	failed, _ := fixture.service.GetAgentSession(context.Background(), view.ID)
	if failed.Status != SessionStatusFailed {
		t.Fatalf("status after a failed round = %s", failed.Status)
	}
	if _, err := fixture.send(t, view.ID, "Versuch es nochmal"); err != nil {
		t.Fatalf("a failed round must not end the session: %v", err)
	}
	if _, err := fixture.send(t, view.ID, " "); !errors.Is(err, ErrInvalidInput) && !errors.Is(err, ErrRunStateConflict) {
		t.Fatalf("empty message = %v", err)
	}
}

func TestMessageValidation(t *testing.T) {
	t.Parallel()
	fixture := newSessionFixture(t)
	view := fixture.start(t, "")
	fixture.finishTurn(t, AgentRunStatusSucceeded, "Fertig", "id-1")
	for _, text := range []string{"", "   ", strings.Repeat("x", 8001)} {
		if _, err := fixture.send(t, view.ID, text); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("send(%d chars) = %v, want invalid input", len(text), err)
		}
	}
}

func TestOpenOnMacNeedsAKnownAgentSession(t *testing.T) {
	t.Parallel()
	fixture := newSessionFixture(t)
	ctx := context.Background()
	view := fixture.start(t, "")
	open := func() (AgentSessionView, error) {
		return fixture.service.OpenAgentSessionOnMac(ctx, fixture.owner, AgentSessionActionInput{
			SessionID: view.ID, MutationMetadata: MutationMetadata{ClientRequestID: requestID()},
		})
	}
	if _, err := open(); !errors.Is(err, ErrRunStateConflict) {
		t.Fatalf("open while working = %v", err)
	}
	fixture.finishTurn(t, AgentRunStatusFailed, "kein Start", "")
	if _, err := open(); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("open without a CLI session = %v", err)
	}
	if _, err := fixture.send(t, view.ID, "Nochmal"); err != nil {
		t.Fatal(err)
	}
	fixture.finishTurn(t, AgentRunStatusSucceeded, "Fertig", "sess-9")
	opened, err := open()
	if err != nil {
		t.Fatalf("OpenAgentSessionOnMac() error = %v", err)
	}
	turn := opened.Turns[len(opened.Turns)-1]
	if turn.TurnKind != TurnKindOpenTerminal || turn.TaskContract.ResumeSessionID != "sess-9" || turn.TaskContract.TurnKind != string(TurnKindOpenTerminal) {
		t.Fatalf("open round = %+v", turn)
	}
	fixture.finishTurn(t, AgentRunStatusSucceeded, "Im Terminal geöffnet", "")
	after, _ := fixture.service.GetAgentSession(ctx, view.ID)
	if after.Status != SessionStatusWaiting || after.HarnessSessionID != "sess-9" {
		t.Fatalf("after opening = %s %q", after.Status, after.HarnessSessionID)
	}
}

func TestClosedSessionAcceptsNothing(t *testing.T) {
	t.Parallel()
	fixture := newSessionFixture(t)
	ctx := context.Background()
	view := fixture.start(t, "")
	fixture.finishTurn(t, AgentRunStatusSucceeded, "Fertig", "id-1")
	closed, err := fixture.service.CloseAgentSession(ctx, fixture.owner, AgentSessionActionInput{
		SessionID: view.ID, MutationMetadata: MutationMetadata{ClientRequestID: requestID()},
	})
	if err != nil || !closed.Closed || closed.Status != SessionStatusClosed {
		t.Fatalf("close = %+v, %v", closed.AgentSession, err)
	}
	if _, err := fixture.send(t, view.ID, "Noch was"); !errors.Is(err, ErrRunStateConflict) {
		t.Fatalf("message to a closed session = %v", err)
	}
	sessions, err := fixture.service.ListAgentSessions(ctx, 20)
	if err != nil || len(sessions) != 1 || sessions[0].Status != SessionStatusClosed {
		t.Fatalf("list = %+v, %v", sessions, err)
	}
}

func TestStartNeedsAnEnabledPolicyAndIsIdempotent(t *testing.T) {
	t.Parallel()
	fixture := newSessionFixture(t)
	ctx := context.Background()
	input := StartAgentSessionInput{ReminderID: fixture.reminder.ID, PolicyID: fixture.policy.ID, MutationMetadata: MutationMetadata{ClientRequestID: requestID()}}
	first, err := fixture.service.StartAgentSession(ctx, fixture.owner, input)
	if err != nil {
		t.Fatal(err)
	}
	again, err := fixture.service.StartAgentSession(ctx, fixture.owner, input)
	if err != nil || again.ID != first.ID || len(again.Turns) != 1 {
		t.Fatalf("retry created another session: %v %v", again.ID, err)
	}
	disabled := false
	if _, err := fixture.service.UpdatePolicy(ctx, fixture.owner, fixture.policy.ID, UpdatePolicyInput{Enabled: &disabled, ExpectedRevision: fixture.policy.Revision, ClientRequestID: requestID()}); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.StartAgentSession(ctx, fixture.owner, StartAgentSessionInput{ReminderID: fixture.reminder.ID, PolicyID: fixture.policy.ID, MutationMetadata: MutationMetadata{ClientRequestID: requestID()}}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("start with a disabled policy = %v", err)
	}
}

func TestSessionRoundsAlwaysNotifyAndRunnersSeeNoSessionEvents(t *testing.T) {
	t.Parallel()
	notified := make([]AgentRun, 0)
	repository := NewMemoryRepository()
	service := NewService(repository, WithClock(func() time.Time { return executionNow }), WithRunNotifier(func(_ context.Context, run AgentRun, _ string) error {
		notified = append(notified, run)
		return nil
	}))
	owner, runner, _ := executionActors()
	project := mustCreateProject(t, service, owner, "karla-report")
	policy := mustCreatePolicy(t, service, owner, project.ID, func(input *CreatePolicyInput) {
		input.Adapter = "claude-code"
		input.NotifyOnCompletion = false
		input.NotifyOnFailure = false
	})
	mustRegisterRunner(t, service, runner, []string{project.ID}, []string{"claude-code"})
	reminder := mustCreateExecutableReminder(t, service, owner, nil)
	fixture := sessionFixture{service: service, owner: owner, runner: runner, reminder: reminder, policy: policy}
	view := fixture.start(t, "")
	fixture.finishTurn(t, AgentRunStatusSucceeded, "Fertig", "id-1")
	if len(notified) != 1 || notified[0].SessionID != view.ID {
		t.Fatalf("notified = %+v", notified)
	}
	changes, _ := repository.ListChanges(context.Background(), 0, 500)
	sawStart := false
	for _, change := range changes {
		if change.Event.Action == AuditActionAgentSessionStarted {
			sawStart = true
		}
	}
	if !sawStart {
		t.Fatal("session start is not audited")
	}
	for _, change := range VisibleChanges(runner, changes) {
		if strings.HasPrefix(string(change.Event.Action), "agent_session.") {
			t.Fatalf("runner sees %s", change.Event.Action)
		}
	}
}

func TestResultTextIsRedactedAndBounded(t *testing.T) {
	t.Parallel()
	fixture := newSessionFixture(t)
	fixture.start(t, "")
	text := "Fertig\napi_key = sk-secret-value\n" + strings.Repeat("a", 30000)
	completed := fixture.finishTurn(t, AgentRunStatusSucceeded, text, "id-1")
	if strings.Contains(completed.ResultText, "sk-secret-value") || len([]rune(completed.ResultText)) > MaxRunResultTextRunes {
		t.Fatalf("result text not redacted or bounded: %d runes", len([]rune(completed.ResultText)))
	}
	if _, err := fixture.service.CompleteAgentRun(context.Background(), fixture.runner, CompleteRunInput{RunID: "x", HarnessSessionID: "bad id with spaces", Outcome: AgentRunStatusSucceeded, ExpectedRevision: 1, MutationMetadata: MutationMetadata{ClientRequestID: requestID()}}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("invalid CLI session ID = %v", err)
	}
}

func TestContractsWithoutSessionsKeepTheirHash(t *testing.T) {
	t.Parallel()
	contract := TaskContract{
		RunID: "01989d9b-0000-7000-8000-000000000001", CorrelationID: "01989d9b-0000-7000-8000-000000000001",
		Objective: "Review", ProjectID: "p", ProjectName: "customer-api", PolicyID: "pol", PolicyRevision: 3,
		AllowedCapabilities: []string{"read_repository"}, TimeoutMinutes: 30,
	}
	// Computed with the code before sessions existed; runs stored then must
	// still verify.
	const want = "c2fc2d23b2fef507bbaa0413da556490d8039e56a7d4afb11afbdd1a2f2a9a8a"
	if got := contract.ComputeHash(); got != want {
		t.Fatalf("hash = %s, want %s", got, want)
	}
}
