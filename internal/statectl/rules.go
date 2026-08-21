package statectl

import "strings"

const (
	RuleBlockStart = "<!-- statectl:state:start -->"
	RuleBlockEnd   = "<!-- statectl:state:end -->"
)

func UpsertRuleBlock(existing string, rules string) string {
	base := strings.TrimRight(RemoveRuleBlock(existing), "\n")
	block := RuleBlockStart + "\n" + strings.TrimSpace(rules) + "\n" + RuleBlockEnd
	if base == "" {
		return block + "\n"
	}
	return base + "\n\n" + block + "\n"
}

func RemoveRuleBlock(existing string) string {
	start := strings.Index(existing, RuleBlockStart)
	if start < 0 {
		return existing
	}
	endOffset := strings.Index(existing[start:], RuleBlockEnd)
	if endOffset < 0 {
		return existing
	}
	end := start + endOffset + len(RuleBlockEnd)
	for end < len(existing) && existing[end] == '\n' {
		end++
	}
	before := strings.TrimRight(existing[:start], "\n")
	after := strings.TrimLeft(existing[end:], "\n")
	if before == "" {
		return after
	}
	if after == "" {
		return before + "\n"
	}
	return before + "\n\n" + after
}

func DefaultAgentRules() string {
	return `State is the durable reminder and context system for this user.

- At session start, call State get_briefing with the last known cursor when available.
- When the user explicitly asks to be reminded, create exactly one State reminder automatically.
- Include the relevant original user wording in source_text.
- Use a stable UUIDv7 client_request_id for every mutation and reuse it for retries.
- Report success only after State confirms stored=true.
- Read the latest reminder revision before editing and pass expected_revision.
- Use add_comment when new context should be appended without replacing the reminder.
- Never delete or archive reminders through an agent.

When launched as a task by a State runner:

- The task prompt names the State reminder ID; read context with get_reminder and get_changes. get_execution_context is reserved for the runner itself.
- When the run's execution policy handles completion, State completes the occurrence automatically on verified success — never complete that occurrence manually.`
}
