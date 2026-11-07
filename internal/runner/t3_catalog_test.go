package runner

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nicremo/state/internal/state"
)

func writeFile(t *testing.T, path string, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestReadT3CatalogKeepsEnabledReadyVisibleModels(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	caches := filepath.Join(home, ".t3", "caches")
	writeFile(t, filepath.Join(caches, "claudeAgent_claude_hub.json"), `{"instanceId":"claudeAgent_claude_hub","driver":"claudeAgent","displayName":"Hub","enabled":true,"status":"ready","auth":{"secret":"never read"},"models":[{"slug":"claude-opus-5-5","name":"Claude Opus 5.5","isDefault":true},{"slug":"claude-sonnet-5-5","name":"Claude Sonnet 5.5"},{"slug":"claude-opus-4-5","name":"Claude Opus 4.5"}]}`)
	writeFile(t, filepath.Join(caches, "acpRegistry_dsh.json"), `{"instanceId":"acpRegistry_dsh","driver":"acpRegistry","displayName":"DSH","enabled":true,"status":"ready","models":[{"slug":"[\"deepseek-official\",\"deepseek-flash\"]","name":"DeepSeek-V4.1-Flash"}]}`)
	writeFile(t, filepath.Join(caches, "claudeAgent.json"), `{"instanceId":"claudeAgent","driver":"claudeAgent","displayName":"Fabian","enabled":true,"status":"ready","models":[{"slug":"claude-opus-5-5","name":"Claude Opus 5.5"}]}`)
	writeFile(t, filepath.Join(caches, "cursor.json"), `{"instanceId":"cursor","driver":"cursor","displayName":"Cursor","enabled":false,"status":"disabled","models":[{"slug":"x","name":"x"}]}`)
	writeFile(t, filepath.Join(caches, "codex.json"), `{"instanceId":"codex","driver":"codex","displayName":"Codex","enabled":true,"status":"error","models":[{"slug":"gpt-6.1-sol","name":"GPT-6.1-Sol"}]}`)
	writeFile(t, filepath.Join(caches, "acp-auth-123.json"), `{"token":"never read"}`)
	writeFile(t, filepath.Join(caches, "acpRegistry_kimi_cli.json"), `{"instanceId":"acpRegistry_kimi_cli","driver":"acpRegistry","displayName":"Kimi CLI","enabled":true,"status":"ready","models":[{"slug":"default","name":"Default"}]}`)
	writeFile(t, filepath.Join(home, ".t3", "userdata", "settings.json"), `{"providerInstances":{"claudeAgent":{"enabled":false},"claudeAgent_claude_hub":{"enabled":true},"acpRegistry_dsh":{"enabled":true,"config":{"agentId":"DeepSeek Harness"}}},"providerModelPreferences":{"claudeAgent_claude_hub":{"hiddenModels":["claude-opus-4-5"]}}}`)

	catalog, err := readT3Catalog(home)
	if err != nil {
		t.Fatal(err)
	}
	got := make([]string, 0, len(catalog))
	for _, model := range catalog {
		got = append(got, model.ProviderName+"/"+model.ModelName)
	}
	want := "DeepSeek Harness/DeepSeek-V4.1-Flash, Claude Hub/Claude Opus 5.5, Claude Hub/Claude Sonnet 5.5"
	if strings.Join(got, ", ") != want {
		t.Fatalf("catalog = %s\nwant      %s", strings.Join(got, ", "), want)
	}
	if !catalog[1].Default || catalog[1].Provider != "claudeAgent_claude_hub" {
		t.Fatalf("hub default = %+v", catalog[1])
	}
}

func TestReadT3CatalogWithoutT3IsEmpty(t *testing.T) {
	t.Parallel()
	catalog, err := readT3Catalog(t.TempDir())
	if err != nil || len(catalog) != 0 {
		t.Fatalf("catalog = %+v, %v", catalog, err)
	}
}

func TestT3PrefersTheServiceBinary(t *testing.T) {
	home := t.TempDir()
	writeFile(t, filepath.Join(home, ".t3", "runtime", "service-state.json"), `{"protocol":3,"activeVersion":"0.1.7"}`)
	service := filepath.Join(home, ".t3", "runtime", "versions", "0.1.7", "t3")
	writeFile(t, service, "#!/bin/sh\n")
	if err := os.Chmod(service, 0o755); err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	fakeT3(t, bin, "ok")
	t.Setenv("PATH", bin+string(os.PathListSeparator)+"/usr/bin:/bin")
	adapter := &t3Adapter{home: home}
	if path, err := adapter.binary(); err != nil || path != service {
		t.Fatalf("binary() = %q, %v", path, err)
	}
	// A version folder name never escapes the runtime folder.
	writeFile(t, filepath.Join(home, ".t3", "runtime", "service-state.json"), `{"activeVersion":"../../bin"}`)
	if path, _ := adapter.binary(); path == service || strings.Contains(path, "..") {
		t.Fatalf("escaping version accepted: %q", path)
	}
}

func TestT3ChosenModelBeatsTheNewestThread(t *testing.T) {
	contract := fullRightsContract()
	contract.ModelProvider = "codex"
	contract.Model = "gpt-6.1-sol"
	contract.ContractHash = contract.ComputeHash()
	result, calls := runT3(t, "withthreads", contract)
	if result.ExitCode != 0 || !strings.Contains(calls, " --provider codex --model gpt-6.1-sol ") || strings.Contains(calls, "claude-opus-5-5") {
		t.Fatalf("calls:\n%s", calls)
	}
}

var _ = state.HarnessModel{}
