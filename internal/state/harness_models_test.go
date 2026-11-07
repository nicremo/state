package state

import (
	"context"
	"errors"
	"testing"
)

func TestRunnerReportsItsHarnessModels(t *testing.T) {
	t.Parallel()

	service, _, _ := executionService(t)
	owner, runnerActor, _ := executionActors()
	ctx := context.Background()
	project := mustCreateProject(t, service, owner, "karla-report")
	mustRegisterRunner(t, service, runnerActor, []string{project.ID}, []string{"t3-code"})
	models := []HarnessModel{
		{Provider: "claudeAgent_claude_hub", ProviderName: "Hub", Model: "claude-sonnet-5-5", ModelName: "Claude Sonnet 5.5"},
		{Provider: "codex", ProviderName: "Codex", Model: "gpt-6.1-sol", ModelName: "GPT-6.1-Sol", Default: true},
	}

	reported, err := service.ReportHarnessModels(ctx, runnerActor, ReportHarnessModelsInput{Models: models, ClientRequestID: requestID()})
	if err != nil || len(reported.HarnessModels) != 2 {
		t.Fatalf("report = %+v, %v", reported.HarnessModels, err)
	}
	again, err := service.ReportHarnessModels(ctx, runnerActor, ReportHarnessModelsInput{Models: models, ClientRequestID: requestID()})
	if err != nil || again.Revision != reported.Revision {
		t.Fatalf("unchanged report wrote a revision: %d -> %d, %v", reported.Revision, again.Revision, err)
	}
	if _, err := service.ReportHarnessModels(ctx, owner, ReportHarnessModelsInput{Models: models, ClientRequestID: requestID()}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("owner report error = %v", err)
	}
	bad := []HarnessModel{{Provider: "codex", ProviderName: "Codex", Model: "with space", ModelName: "x"}}
	if _, err := service.ReportHarnessModels(ctx, runnerActor, ReportHarnessModelsInput{Models: bad, ClientRequestID: requestID()}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("invalid model error = %v", err)
	}

	// Registering again (new scopes) keeps the catalog.
	registered := mustRegisterRunner(t, service, runnerActor, []string{project.ID}, []string{"t3-code", "codex"})
	if len(registered.HarnessModels) != 2 {
		t.Fatalf("catalog after register = %+v", registered.HarnessModels)
	}
}
