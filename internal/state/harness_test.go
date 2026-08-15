package state

import "testing"

func TestValidHarnessAcceptsSlugs(t *testing.T) {
	accepted := []string{
		"codex",
		"claude-code",
		"opencode",
		"pi",
		"my-own-agent",
		"agent7",
		"ab",
		"a234567890123456789012345678901b",
	}
	for _, harness := range accepted {
		if !ValidHarness(harness) {
			t.Fatalf("expected %q to be a valid harness", harness)
		}
	}
}

func TestValidHarnessRejectsAmbiguousLabels(t *testing.T) {
	rejected := []string{
		"",
		"a",
		"Codex",
		"claude code",
		"-codex",
		"codex-",
		"codex_cli",
		"agent.one",
		"a234567890123456789012345678901bc",
		" codex",
	}
	for _, harness := range rejected {
		if ValidHarness(harness) {
			t.Fatalf("expected %q to be rejected", harness)
		}
	}
}

func TestKnownHarnessReportsShippedIntegrations(t *testing.T) {
	for _, harness := range KnownHarnesses() {
		if !ValidHarness(harness) {
			t.Fatalf("shipped harness %q must satisfy the slug rule", harness)
		}
		if !KnownHarness(harness) {
			t.Fatalf("expected %q to be reported as known", harness)
		}
	}
	if KnownHarness("pi") {
		t.Fatal("pi has no shipped integration and must not be reported as known")
	}
}
