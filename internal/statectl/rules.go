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
	return `State is the owner's shared memory for tasks and reminders, used by all of his agents (MCP server "state").

- Session start: call get_briefing.
- Explicit task or recurring obligation ("remind me", "every month ..."): ask once when date, time or recurrence is unclear. Never invent a date or recurrence. Then call create_reminder with the owner's words as source_text and a fresh UUIDv7 client_request_id. Report success only after stored=true.
- New context: add_comment. Before update_reminder pass expected_revision. Never store secrets.
- Without MCP: statectl reminder create --profile <profile> --title ... --source-text ... (see statectl reminder --help).
- Launched by a State runner: read the reminder with get_reminder; get_execution_context is reserved for the runner. Never complete that occurrence manually.`
}
