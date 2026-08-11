package statectl

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigStorePersistsProfilesWithoutCredential(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "state", "statectl.json")
	store := NewConfigStore(path)
	profile := Profile{
		Name:      "claude-code",
		ServerURL: "https://state.example.com",
		ActorID:   "01989f31-2956-754b-a101-8c868269de8d",
		Harness:   "claude-code",
	}
	if err := store.SaveProfile(profile); err != nil {
		t.Fatalf("SaveProfile() error = %v", err)
	}
	loaded, err := store.LoadProfile("claude-code")
	if err != nil {
		t.Fatalf("LoadProfile() error = %v", err)
	}
	if loaded != profile {
		t.Fatalf("loaded profile = %#v, want %#v", loaded, profile)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if strings.Contains(string(contents), "state_") || strings.Contains(string(contents), "token") {
		t.Fatalf("config contains credential material: %s", contents)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat() error = %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("config permissions = %o, want 600", info.Mode().Perm())
	}
}

func TestMarkedRuleBlockIsIdempotentAndRemovable(t *testing.T) {
	t.Parallel()

	existing := "# Existing rules\n\nKeep this text.\n"
	installed := UpsertRuleBlock(existing, "Call get_briefing at session start.")
	reinstalled := UpsertRuleBlock(installed, "Call get_briefing at session start.")
	if installed != reinstalled {
		t.Fatalf("rule installation is not idempotent:\n%s", reinstalled)
	}
	if !strings.Contains(installed, existing) || strings.Count(installed, RuleBlockStart) != 1 {
		t.Fatalf("installed rules are invalid:\n%s", installed)
	}
	removed := RemoveRuleBlock(installed)
	if strings.Contains(removed, RuleBlockStart) || !strings.Contains(removed, "Keep this text.") {
		t.Fatalf("removed rules are invalid:\n%s", removed)
	}
}
