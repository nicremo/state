package store

import (
	"context"
	"testing"

	"github.com/nicremo/state/internal/state"
)

func TestScheduledT3SessionIsStoredOnceWithItsOccurrence(t *testing.T) {
	t.Parallel()

	app := bootstrappedApp(t, t.TempDir())
	service, _ := newExecutionService(t, app)
	ctx := context.Background()
	project := mustStoreProject(t, service, "karla-report")
	policy, err := service.CreatePolicy(ctx, executorOwner, state.CreatePolicyInput{
		Name: "karla-report-t3-code", ProjectID: project.ID, Adapter: state.AdapterT3Code, Mode: state.ExecutionModeSupervised,
		AllowedCapabilities: []string{state.CapabilityReadRepository, state.CapabilityEditRepository, state.CapabilityRunTests},
		TimeoutMinutes:      60, ClientRequestID: execRequestID(),
	})
	if err != nil {
		t.Fatal(err)
	}
	reminder := mustStoreReminder(t, service, &policy.ID)
	sonnet := &state.AgentModel{Provider: "claudeAgent_claude_hub", Model: "claude-sonnet-5-5"}
	reminder, err = service.UpdateReminder(ctx, executorOwner, reminder.ID, state.UpdateReminderInput{ExecutionModel: &sonnet, ExpectedRevision: reminder.Revision, ClientRequestID: execRequestID()})
	if err != nil {
		t.Fatal(err)
	}

	turn := mustStoreMaterializedRun(t, service)
	if turn.SessionID == "" || turn.OccurrenceID == nil || turn.TaskContract.Model != "claude-sonnet-5-5" {
		t.Fatalf("turn = %+v", turn)
	}
	again, err := service.MaterializeEligibleRuns(ctx, executionNow)
	if err != nil || len(again) != 0 {
		t.Fatalf("second cycle = %d, %v", len(again), err)
	}
	sessions, err := service.ListAgentSessions(ctx, 10)
	if err != nil || len(sessions) != 1 || sessions[0].Model == nil || len(sessions[0].Turns) != 1 {
		t.Fatalf("sessions = %+v, %v", sessions, err)
	}
	stored, err := service.GetAgentRun(ctx, turn.ID)
	if err != nil || stored.OccurrenceID == nil || *stored.OccurrenceID != *turn.OccurrenceID {
		t.Fatalf("stored run = %+v, %v", stored, err)
	}
}
