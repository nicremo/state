package statectl

import (
	"strings"
	"testing"
)

func TestDefaultAgentRulesCoverCaptureProtocol(t *testing.T) {
	rules := DefaultAgentRules()
	for _, required := range []string{
		"get_briefing",
		"create_reminder",
		"stored=true",
		"source_text",
		"client_request_id",
		"expected_revision",
		"add_comment",
		"Never invent a date or recurrence",
		"statectl reminder create",
		"get_execution_context is reserved for the runner",
		"Never complete that occurrence manually",
	} {
		if !strings.Contains(rules, required) {
			t.Errorf("rules are missing %q", required)
		}
	}
}

func TestDefaultAgentRulesStayBounded(t *testing.T) {
	if length := len(DefaultAgentRules()); length > 4000 {
		t.Fatalf("rules grew to %d bytes; every agent pays for them on each turn", length)
	}
}

func TestDefaultAgentRulesHaveNoDashPunctuation(t *testing.T) {
	rules := DefaultAgentRules()
	if strings.ContainsAny(rules, "–—") {
		t.Fatal("rules must not contain en or em dashes")
	}
}

func TestUpsertRuleBlockIsIdempotent(t *testing.T) {
	once := UpsertRuleBlock("# Existing\n", DefaultAgentRules())
	twice := UpsertRuleBlock(once, DefaultAgentRules())
	if once != twice {
		t.Fatal("upserting the same rules twice changed the file")
	}
}
