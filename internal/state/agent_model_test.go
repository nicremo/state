package state

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestAgentModelValidation(t *testing.T) {
	t.Parallel()

	valid := []AgentModel{
		{Provider: "claudeAgent_claude_hub", Model: "claude-sonnet-5-5"},
		{Provider: "codex", Model: "gpt-6.1-sol"},
		{Provider: "acpRegistry_kimi", Model: "kimi-code/k3.1-pro"},
		{Provider: "acpRegistry_dsh", Model: `["deepseek-official","deepseek-flash"]`},
	}
	for _, model := range valid {
		if !model.Valid() {
			t.Errorf("Valid(%+v) = false", model)
		}
	}
	invalid := []AgentModel{
		{},
		{Provider: "codex"},
		{Model: "gpt-6.1-sol"},
		{Provider: "bad provider", Model: "x"},
		{Provider: "codex", Model: "line\nbreak"},
		{Provider: "codex", Model: strings.Repeat("m", 201)},
	}
	for _, model := range invalid {
		if model.Valid() {
			t.Errorf("Valid(%+v) = true", model)
		}
	}
}

func TestReminderStoresAndClearsItsExecutionModel(t *testing.T) {
	t.Parallel()

	service, _, _ := executionService(t)
	owner, _, _ := executionActors()
	ctx := context.Background()
	sonnet := &AgentModel{Provider: "claudeAgent_claude_hub", Model: "claude-sonnet-5-5"}

	created, err := service.CreateReminder(ctx, owner, CreateReminderInput{Title: "Karla", ExecutionModel: sonnet, ClientRequestID: requestID()})
	if err != nil || created.ExecutionModel == nil || *created.ExecutionModel != *sonnet {
		t.Fatalf("created = %+v, %v", created.ExecutionModel, err)
	}
	if _, err := service.CreateReminder(ctx, owner, CreateReminderInput{Title: "Bad", ExecutionModel: &AgentModel{Provider: "codex"}, ClientRequestID: requestID()}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("invalid model error = %v", err)
	}

	opus := &AgentModel{Provider: "claudeAgent_claude_hub", Model: "claude-opus-5-5"}
	updated, err := service.UpdateReminder(ctx, owner, created.ID, UpdateReminderInput{ExecutionModel: &opus, ExpectedRevision: created.Revision, ClientRequestID: requestID()})
	if err != nil || updated.ExecutionModel == nil || updated.ExecutionModel.Model != "claude-opus-5-5" {
		t.Fatalf("updated = %+v, %v", updated.ExecutionModel, err)
	}
	var cleared *AgentModel
	updated, err = service.UpdateReminder(ctx, owner, created.ID, UpdateReminderInput{ExecutionModel: &cleared, ExpectedRevision: updated.Revision, ClientRequestID: requestID()})
	if err != nil || updated.ExecutionModel != nil {
		t.Fatalf("cleared = %+v, %v", updated.ExecutionModel, err)
	}
}

func TestSessionModelReachesTheContract(t *testing.T) {
	t.Parallel()

	fixture := newSessionFixture(t)
	t3 := mustCreatePolicy(t, fixture.service, fixture.owner, fixture.policy.ProjectID, func(input *CreatePolicyInput) {
		input.Name = "karla-t3"
		input.Adapter = "t3-code"
		input.AllowedCapabilities = []string{CapabilityReadRepository, CapabilityEditRepository, CapabilityRunTests}
	})
	ctx := context.Background()
	model := &AgentModel{Provider: "codex", Model: "gpt-6.1-sol"}

	view, err := fixture.service.StartAgentSession(ctx, fixture.owner, StartAgentSessionInput{
		ReminderID: fixture.reminder.ID, PolicyID: t3.ID, Model: model,
		MutationMetadata: MutationMetadata{ClientRequestID: requestID()},
	})
	if err != nil {
		t.Fatalf("StartAgentSession() error = %v", err)
	}
	if view.Model == nil || *view.Model != *model {
		t.Fatalf("session model = %+v", view.Model)
	}
	contract := view.Turns[0].TaskContract
	if contract.ModelProvider != "codex" || contract.Model != "gpt-6.1-sol" || contract.ContractHash != contract.ComputeHash() {
		t.Fatalf("contract = %+v", contract)
	}

	// A model only means something to T3 Code; other agents pick their own.
	if _, err := fixture.service.StartAgentSession(ctx, fixture.owner, StartAgentSessionInput{
		ReminderID: fixture.reminder.ID, PolicyID: fixture.policy.ID, Model: model,
		MutationMetadata: MutationMetadata{ClientRequestID: requestID()},
	}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("model on a claude-code policy error = %v", err)
	}
}

func TestContractWithoutModelKeepsItsHash(t *testing.T) {
	t.Parallel()

	contract := TaskContract{RunID: "r", CorrelationID: "c", Objective: "o", ProjectID: "p", ProjectName: "n", PolicyID: "pol", PolicyRevision: 1, AllowedCapabilities: []string{"read_repository"}, TimeoutMinutes: 30}
	// Recorded before model fields existed.
	const before = "bf008e65ea19da41bb4ba30e182d953e18e50d846b63b8d1d097a616c3ad2185"
	if got := contract.ComputeHash(); got != before {
		t.Fatalf("hash = %s, want %s", got, before)
	}
}
