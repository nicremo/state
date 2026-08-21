package state

import (
	"strings"
	"testing"
)

func TestValidProjectSlugAcceptsSlugs(t *testing.T) {
	t.Parallel()

	accepted := []string{
		"ab",
		"a1",
		"customer-api",
		"codex",
		"a" + strings.Repeat("b2", 31) + "c", // exactly 64 characters
	}
	for _, slug := range accepted {
		if !ValidProjectSlug(slug) {
			t.Fatalf("expected %q to be a valid project slug", slug)
		}
	}
}

func TestValidProjectSlugRejectsAmbiguousNames(t *testing.T) {
	t.Parallel()

	rejected := []string{
		"",
		"a",
		"Customer-Api",
		"customer api",
		"-customer-api",
		"customer-api-",
		"customer_api",
		"customer.api",
		"a234567890123456789012345678901b234567890123456789012345678901234b",
		" customer-api",
	}
	for _, slug := range rejected {
		if ValidProjectSlug(slug) {
			t.Fatalf("expected %q to be rejected", slug)
		}
	}
}

func TestTaskContractComputeHashIsStableAndComplete(t *testing.T) {
	t.Parallel()

	contract := TaskContract{
		RunID:               "01989d9b-b5c4-7f9e-88f2-4347a1e90f16",
		CorrelationID:       "01989d9b-b5c4-7f9e-88f2-4347a1e90f16",
		Objective:           "Review the deployment",
		AcceptanceCriteria:  []string{"All tests pass"},
		ProjectID:           "01989d9b-b5c4-7000-8000-000000000001",
		ProjectName:         "customer-api",
		PolicyID:            "01989d9b-b5c4-7000-8000-000000000002",
		PolicyRevision:      3,
		AllowedCapabilities: []string{CapabilityReadRepository, CapabilityRunTests},
		TimeoutMinutes:      30,
		CompletionRule:      CompletionRuleMarkOccurrenceDoneOnSuccess,
	}

	first := contract.ComputeHash()
	if first == "" {
		t.Fatal("ComputeHash() is empty")
	}
	if first != contract.ComputeHash() {
		t.Fatal("ComputeHash() is not deterministic")
	}

	// The hash field itself never feeds the hash.
	contract.ContractHash = first
	if contract.ComputeHash() != first {
		t.Fatal("ComputeHash() must exclude the ContractHash field")
	}

	tampered := contract
	tampered.Objective = "Deploy to production"
	tampered.ContractHash = ""
	if tampered.ComputeHash() == first {
		t.Fatal("ComputeHash() must change with the contract content")
	}
}
