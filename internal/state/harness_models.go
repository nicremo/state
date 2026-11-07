package state

import (
	"context"
	"fmt"
	"reflect"
	"unicode"
	"unicode/utf8"
)

// HarnessModel is one model a runner's harness offers, as the runner read it
// from the harness (T3 Code's provider caches). The app shows the catalog so
// the owner picks a model without knowing provider instance IDs.
type HarnessModel struct {
	Provider     string `json:"provider"`
	ProviderName string `json:"provider_name"`
	Model        string `json:"model"`
	ModelName    string `json:"model_name"`
	Default      bool   `json:"default,omitempty"`
}

// ReportHarnessModelsInput replaces the runner's catalog.
type ReportHarnessModelsInput struct {
	Models          []HarnessModel `json:"models"`
	ClientRequestID string         `json:"client_request_id"`
}

const (
	maxHarnessModels        = 200
	maxHarnessDisplayLength = 120
)

func validHarnessDisplayName(name string) bool {
	if name == "" || utf8.RuneCountInString(name) > maxHarnessDisplayLength || !utf8.ValidString(name) {
		return false
	}
	for _, character := range name {
		if unicode.IsControl(character) {
			return false
		}
	}
	return true
}

func (model HarnessModel) valid() bool {
	return AgentModel{Provider: model.Provider, Model: model.Model}.Valid() &&
		validHarnessDisplayName(model.ProviderName) && validHarnessDisplayName(model.ModelName)
}

// ReportHarnessModels stores the catalog a runner read from its harness. An
// unchanged catalog writes nothing, so the runner may report it often.
func (service *Service) ReportHarnessModels(ctx context.Context, actor Actor, input ReportHarnessModelsInput) (Runner, error) {
	if actor.Kind != ActorKindRunner {
		return Runner{}, ErrForbidden
	}
	if actor.ID == "" || input.ClientRequestID == "" || len(input.Models) > maxHarnessModels {
		return Runner{}, ErrInvalidInput
	}
	models := make([]HarnessModel, 0, len(input.Models))
	seen := make(map[string]bool, len(input.Models))
	for _, model := range input.Models {
		if !model.valid() {
			return Runner{}, ErrInvalidInput
		}
		key := model.Provider + "\x00" + model.Model
		if seen[key] {
			continue
		}
		seen[key] = true
		models = append(models, model)
	}
	current, err := service.repository.GetRunner(ctx, actor.ID)
	if err != nil {
		return Runner{}, err
	}
	if reflect.DeepEqual(current.HarnessModels, models) || (len(current.HarnessModels) == 0 && len(models) == 0) {
		return current, nil
	}
	updated := current
	updated.HarnessModels = models
	updated.Revision++
	eventID, err := service.newID()
	if err != nil {
		return Runner{}, fmt.Errorf("generate audit event ID: %w", err)
	}
	event, err := service.buildAuditEvent(eventID, "", AuditActionRunnerUpdated, actor, service.clock().UTC(), nil, "state-runner", "", &current, updated, []string{"harness_models"}, updated.Revision, "", input.ClientRequestID)
	if err != nil {
		return Runner{}, err
	}
	return service.repository.UpdateRunner(ctx, updated, current.Revision, event, input.ClientRequestID)
}
