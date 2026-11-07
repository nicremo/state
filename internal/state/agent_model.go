package state

import (
	"regexp"
	"unicode"
	"unicode/utf8"
)

// AgentModel names a model in a harness that hosts several providers, today
// T3 Code: Provider is the harness's provider instance (for example
// claudeAgent_claude_hub or codex) and Model the provider's model slug (for
// example claude-sonnet-5-5). The runner passes both to the harness as
// separate arguments, never through a shell.
type AgentModel struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
}

var agentProviderPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:-]{0,63}$`)

// maxAgentModelRunes bounds a model slug. Slugs are short, but ACP agents
// may use JSON-like identifiers such as ["deepseek-official","deepseek-flash"].
const maxAgentModelRunes = 200

// Valid reports whether both parts are set and safe to store and pass on.
func (model AgentModel) Valid() bool {
	if !agentProviderPattern.MatchString(model.Provider) {
		return false
	}
	if model.Model == "" || utf8.RuneCountInString(model.Model) > maxAgentModelRunes || !utf8.ValidString(model.Model) {
		return false
	}
	for _, character := range model.Model {
		if unicode.IsControl(character) || unicode.IsSpace(character) {
			return false
		}
	}
	return true
}

// validAgentModelPointer accepts an absent model and rejects a malformed one.
func validAgentModelPointer(model *AgentModel) bool {
	return model == nil || model.Valid()
}

func cloneAgentModel(model *AgentModel) *AgentModel {
	if model == nil {
		return nil
	}
	copy := *model
	return &copy
}

// AdapterT3Code is the runner adapter that runs agent rounds as T3 Code
// threads. It is the only adapter that understands an AgentModel.
const AdapterT3Code = "t3-code"
