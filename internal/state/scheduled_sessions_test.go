package state

import (
	"context"
	"strings"
	"testing"
	"time"
)

// mustCreateDailyBacklog creates a daily reminder on Aug 19 and moves the
// clock to the fixture time, so three days are due (Aug 19, 20 and 21,
// 10:00 UTC), as when the Mac was off for two days.
func mustCreateDailyBacklog(t *testing.T, service *Service, advance func(time.Time), actor Actor, policyID string, model *AgentModel) Reminder {
	t.Helper()
	advance(time.Date(2026, time.August, 19, 9, 0, 0, 0, time.UTC))
	defer advance(executionNow)
	reminder, err := service.CreateReminder(context.Background(), actor, CreateReminderInput{
		Title:             "Mac-Tagespflege",
		Description:       "Führe den Skill mac-tagespflege aus.",
		ExecutionPolicyID: &policyID,
		ExecutionModel:    model,
		ClientRequestID:   requestID(),
		Schedule:          &Schedule{LocalDate: "2026-08-19", LocalTime: "10:00", TimeZone: "UTC", Mode: TimeZoneModeFixed},
		Recurrence:        &RecurrenceRule{Frequency: RecurrenceDaily, Interval: 1},
	})
	if err != nil {
		t.Fatalf("CreateReminder() error = %v", err)
	}
	return reminder
}

func TestDueT3ReminderStartsOneSessionForItsNewestOccurrence(t *testing.T) {
	t.Parallel()

	service, _, advance := executionService(t)
	owner, _, _ := executionActors()
	ctx := context.Background()
	project := mustCreateProject(t, service, owner, "hardware")
	policy := mustCreatePolicy(t, service, owner, project.ID, func(input *CreatePolicyInput) {
		input.Name = "hardware-t3"
		input.Adapter = AdapterT3Code
		input.AllowedCapabilities = []string{CapabilityReadRepository, CapabilityEditRepository, CapabilityRunTests}
	})
	sonnet := &AgentModel{Provider: "claudeAgent_claude_hub", Model: "claude-sonnet-5-5"}
	reminder := mustCreateDailyBacklog(t, service, advance, owner, policy.ID, sonnet)

	created, err := service.MaterializeEligibleRuns(ctx, executionNow)
	if err != nil {
		t.Fatalf("MaterializeEligibleRuns() error = %v", err)
	}
	if len(created) != 1 {
		t.Fatalf("created %d runs, want one session round for the newest occurrence", len(created))
	}
	turn := created[0]
	if turn.SessionID == "" || turn.TurnKind != TurnKindMessage || turn.OccurrenceID == nil || turn.CreatedByActor.Kind != ActorKindSystem {
		t.Fatalf("round = %+v", turn)
	}
	occurrence, err := service.repository.GetOccurrence(ctx, *turn.OccurrenceID)
	if err != nil || occurrence.LocalDate != "2026-08-21" {
		t.Fatalf("occurrence = %+v, %v", occurrence, err)
	}
	contract := turn.TaskContract
	if contract.ModelProvider != sonnet.Provider || contract.Model != sonnet.Model || contract.ContractHash != contract.ComputeHash() {
		t.Fatalf("contract = %+v", contract)
	}
	for _, want := range []string{"State reminder ID: " + reminder.ID, "Due: 2026-08-21 10:00", "2026-08-20", "2026-08-19"} {
		if !strings.Contains(contract.Objective, want) {
			t.Fatalf("objective misses %q:\n%s", want, contract.Objective)
		}
	}
	session, err := service.GetAgentSession(ctx, turn.SessionID)
	if err != nil || session.Model == nil || session.CreatedByActor.Kind != ActorKindSystem || session.Adapter != AdapterT3Code {
		t.Fatalf("session = %+v, %v", session.AgentSession, err)
	}

	// The next cycle neither repeats the newest occurrence nor walks down the
	// backlog.
	again, err := service.MaterializeEligibleRuns(ctx, executionNow)
	if err != nil || len(again) != 0 {
		t.Fatalf("second cycle = %d, %v", len(again), err)
	}
}

func TestBareRunsAlsoRunOnlyTheNewestDueOccurrence(t *testing.T) {
	t.Parallel()

	service, _, advance := executionService(t)
	owner, _, _ := executionActors()
	project := mustCreateProject(t, service, owner, "customer-api")
	policy := mustCreatePolicy(t, service, owner, project.ID, nil)
	mustCreateDailyBacklog(t, service, advance, owner, policy.ID, nil)

	run := mustMaterializeOne(t, service, executionNow)
	if run.SessionID != "" || !strings.Contains(run.TaskContract.Objective, "Mac-Tagespflege") {
		t.Fatalf("bare run = %+v", run)
	}
}
