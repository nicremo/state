package runner

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/nicremo/state/internal/state"
)

// T3 Code keeps one status file per provider instance in ~/.t3/caches and the
// owner's choices (disabled instances, hidden models) in
// ~/.t3/userdata/settings.json. The runner reads both to tell the State app
// which models it can pick. Only names and IDs leave the Mac; auth and
// environment fields are never read into the catalog.

type t3ProviderCache struct {
	InstanceID  string `json:"instanceId"`
	Driver      string `json:"driver"`
	DisplayName string `json:"displayName"`
	Enabled     bool   `json:"enabled"`
	Status      string `json:"status"`
	Models      []struct {
		Slug      string `json:"slug"`
		Name      string `json:"name"`
		IsDefault bool   `json:"isDefault"`
	} `json:"models"`
}

type t3Settings struct {
	ProviderInstances map[string]struct {
		Enabled *bool `json:"enabled"`
		Config  struct {
			AgentID string `json:"agentId"`
		} `json:"config"`
	} `json:"providerInstances"`
	ProviderModelPreferences map[string]struct {
		HiddenModels []string `json:"hiddenModels"`
	} `json:"providerModelPreferences"`
}

// readT3Catalog lists the models of every enabled, ready T3 provider
// instance, without the models the owner hid in T3. A missing T3 data folder
// is an empty catalog, not an error.
func readT3Catalog(home string) ([]state.HarnessModel, error) {
	root := filepath.Join(home, ".t3")
	var settings t3Settings
	if contents, err := os.ReadFile(filepath.Join(root, "userdata", "settings.json")); err == nil {
		_ = json.Unmarshal(contents, &settings)
	}
	files, err := filepath.Glob(filepath.Join(root, "caches", "*.json"))
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	catalog := make([]state.HarnessModel, 0)
	for _, file := range files {
		contents, err := os.ReadFile(file)
		if err != nil {
			continue
		}
		var cache t3ProviderCache
		if json.Unmarshal(contents, &cache) != nil || cache.InstanceID == "" || cache.Driver == "" {
			continue
		}
		instance, configured := settings.ProviderInstances[cache.InstanceID]
		// A cache outlives an instance the owner removed in T3. Only the
		// built-in default instance of a driver (codex, claudeAgent) exists
		// without an entry in the settings.
		if !configured && cache.InstanceID != cache.Driver {
			continue
		}
		if !cache.Enabled || cache.Status != "ready" || (configured && instance.Enabled != nil && !*instance.Enabled) {
			continue
		}
		hidden := map[string]bool{}
		for _, slug := range settings.ProviderModelPreferences[cache.InstanceID].HiddenModels {
			hidden[slug] = true
		}
		providerName := t3ProviderName(cache, instance.Config.AgentID)
		for _, model := range cache.Models {
			if model.Slug == "" || hidden[model.Slug] {
				continue
			}
			name := strings.TrimSpace(model.Name)
			if name == "" {
				name = model.Slug
			}
			entry := state.HarnessModel{Provider: cache.InstanceID, ProviderName: providerName, Model: model.Slug, ModelName: name, Default: model.IsDefault}
			if (state.AgentModel{Provider: entry.Provider, Model: entry.Model}).Valid() {
				catalog = append(catalog, entry)
			}
		}
	}
	return catalog, nil
}

// t3ProviderName is how the app labels a provider: Claude instances by their
// T3 name ("Claude Hub"), ACP agents by their agent name when T3 knows one
// ("DeepSeek Harness" for the instance shown as DSH).
func t3ProviderName(cache t3ProviderCache, agentID string) string {
	name := strings.TrimSpace(cache.DisplayName)
	switch {
	case cache.Driver == "claudeAgent" && !strings.Contains(strings.ToLower(name), "claude"):
		return strings.TrimSpace("Claude " + name)
	case cache.Driver == "acpRegistry" && strings.TrimSpace(agentID) != "":
		return strings.TrimSpace(agentID)
	case name == "":
		return cache.InstanceID
	}
	return name
}
