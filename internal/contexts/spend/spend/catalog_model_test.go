package spend

import (
	"strings"
	"testing"

	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// CatalogModel names the row that prices a model: the ID a dated, aliased,
// gateway-namespaced or resold name is priced by, and nothing for a model
// no row prices.
func TestCatalogModel(t *testing.T) {
	table := DefaultTable()
	tests := []struct {
		provider eventschema.Provider
		model    string
		want     Key
		ok       bool
	}{
		{eventschema.ProviderAnthropic, "claude-opus-4-8", Key{eventschema.ProviderAnthropic, "claude-opus-4-8"}, true},
		{eventschema.ProviderAnthropic, "claude-opus-4-8-20261001", Key{eventschema.ProviderAnthropic, "claude-opus-4-8"}, true},
		{eventschema.ProviderAnthropic, "claude-opus-4-8[1m]", Key{eventschema.ProviderAnthropic, "claude-opus-4-8"}, true},
		{eventschema.ProviderOpenAI, "gpt-4o-2024-08-06", Key{eventschema.ProviderOpenAI, "gpt-4o"}, true},
		{eventschema.ProviderOpenAI, "gpt-4o-mini-2024-07-18", Key{eventschema.ProviderOpenAI, "gpt-4o-mini"}, true},
		{"fireworks", "accounts/fireworks/models/kimi-k3", Key{"fireworks", "kimi-k3"}, true},
		// Copilot writes Claude versions with dots; Anthropic's row prices it.
		{eventschema.ProviderGitHub, "claude-opus-4.8", Key{eventschema.ProviderAnthropic, "claude-opus-4-8"}, true},
		{eventschema.ProviderAnthropic, "my-team-finetune", Key{}, false},
		{eventschema.ProviderAnthropic, "", Key{}, false},
		{eventschema.ProviderOllama, "llama3:8b", Key{}, false},
		{"openrouter", "some/model:free", Key{}, false},
	}
	for _, tt := range tests {
		got, ok := table.CatalogModel(tt.provider, tt.model)
		if ok != tt.ok || got != tt.want {
			t.Errorf("CatalogModel(%s, %q) = %+v, %v; want %+v, %v", tt.provider, tt.model, got, ok, tt.want, tt.ok)
		}
	}
}

// Every catalog ID is its own normal form, so a server can check an ID by
// normalising it again.
func TestCatalogModelIsAFixedPoint(t *testing.T) {
	table := DefaultTable()
	for k := range table.Rates {
		id := strings.TrimSuffix(k.Model, "*")
		got, ok := table.CatalogModel(k.Provider, id)
		if !ok || got.Model != id {
			t.Errorf("%s/%s: CatalogModel(%q) = %+v, %v", k.Provider, k.Model, id, got, ok)
		}
	}
}
