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
	return `State is the durable reminder, task and context system for this user. Every paired agent shares it.

Session start
- Call State get_briefing with the last known cursor when available. Keep the returned cursor for the next session.

When to capture
- Capture only explicit intent: "remind me", "I need to", "don't let me forget", "create a task", "schedule", "every month/week/day ...", or a clearly stated obligation with a deadline.
- Do not capture hypotheticals, ideas under discussion, status updates, or things that already happened without a requested follow-up. If unsure, ask once: "Should I add this to State?"

Before writing
- Resolve what changes the meaning: date, local time, time zone, recurrence (unit, interval, day of month), and whether it is a reminder only or should later run as an agent task. Ask one short question when any of these is missing and matters. Never invent a date or recurrence.
- Default time zone and prewarning come from the user's stated preferences or the machine's local zone. Say which defaults you used.

Writing the reminder
- Call create_reminder exactly once per obligation. Title: short and imperative. Description in Markdown with these sections when known: Objective, Why it matters, Project boundary (repository or folder), Procedure and references (paths, URLs, docs), Acceptance criteria, Risk and approvals.
- Put the user's original wording in source_text.
- Use a fresh UUIDv7 client_request_id per new obligation and reuse it for retries of the same write.
- Recurring obligations use recurrence (daily, weekly, monthly, yearly with interval), not several reminders.
- Report success only after State returns stored=true, and name the reminder title and next due date. On any error, say plainly that nothing was saved.

Keeping context current
- Append new decisions, links and blockers with add_comment instead of rewriting the description.
- Before update_reminder, read the reminder and pass its current revision as expected_revision.
- Never delete or archive reminders. Never put secrets, tokens, passwords, private keys or full logs into State.

Without MCP
- If State tools are unavailable, use the terminal: statectl reminder create --profile <profile> --title ... --source-text ... [--date YYYY-MM-DD --time HH:MM --tz Area/City --repeat monthly]. Run statectl reminder --help for all options.

When launched as a task by a State runner
- The task prompt names the State reminder ID. Read context with get_reminder and get_changes. get_execution_context is reserved for the runner itself.
- When the run's execution policy handles completion, State completes the occurrence automatically on verified success. Never complete that occurrence manually.
- Finish with a short result: what changed, how it was verified, what remains open.`
}
