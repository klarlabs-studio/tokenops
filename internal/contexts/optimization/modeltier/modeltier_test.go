package modeltier

import (
	"strings"
	"testing"

	"go.klarlabs.de/tokenops/internal/contexts/spend/spend"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

func table() spend.Table {
	return spend.Table{Currency: "USD", Rates: map[spend.Key]spend.Rate{
		{Provider: "anthropic", Model: "claude-haiku-4-5"}:  {InputPerMillion: 1, OutputPerMillion: 5},
		{Provider: "anthropic", Model: "claude-sonnet-5"}:   {InputPerMillion: 2, OutputPerMillion: 10},
		{Provider: "anthropic", Model: "claude-opus-5"}:     {InputPerMillion: 5, OutputPerMillion: 25},
		{Provider: "anthropic", Model: "claude-fable-5"}:    {InputPerMillion: 10, OutputPerMillion: 50},
		{Provider: "anthropic", Model: "claude-opus-4-6"}:   {InputPerMillion: 5, OutputPerMillion: 25},
		{Provider: "anthropic", Model: "claude-sonnet-4-6"}: {InputPerMillion: 3, OutputPerMillion: 15},
		{Provider: "openai", Model: "gpt-5-mini"}:           {InputPerMillion: 0.25, OutputPerMillion: 2},
		{Provider: "openai", Model: "gpt-5"}:                {InputPerMillion: 1.25, OutputPerMillion: 10},
	}}
}

// A model that costs nothing is the best possible target for cheap work,
// and the old router skipped exactly those: it filtered on cost <= 0, so
// every :free and local model was invisible to the one component whose
// job is finding something cheaper. On one real machine that hid 12 of
// 36 models.
func TestFreeModelsAreSelectable(t *testing.T) {
	c := New(table(), nil)
	for _, m := range []string{
		"glm-5-free", "kimi-k2.5-free", "hy3-preview-free", "minimax-m2.5-free",
	} {
		got := c.Resolve("opencode", m)
		if !got.Free {
			t.Errorf("Resolve(opencode/%s).Free = false, want true", m)
		}
		if got.Tier != TierLookup {
			t.Errorf("Resolve(opencode/%s).Tier = %q, want %q", m, got.Tier, TierLookup)
		}
	}
	// openrouter spells it with a colon.
	if got := c.Resolve("openrouter", "z-ai/glm-4.5-air:free"); !got.Free {
		t.Errorf("openrouter :free suffix not detected: %+v", got)
	}
}

// A local model costs nothing to run and never leaves the machine.
func TestLocalModelsAreFreeLookupTier(t *testing.T) {
	c := New(table(), nil)
	got := c.Resolve("ollama", "qwen2.5-coder:14b")
	if !got.Free || got.Tier != TierLookup {
		t.Errorf("ollama model = %+v, want free lookup tier", got)
	}
	if got.Basis != BasisLocal {
		t.Errorf("Basis = %q, want %q", got.Basis, BasisLocal)
	}
}

// The same model reached through a different front door is the same
// model. Copilot and opencode both proxy Anthropic models under their
// own provider name, and pricing them as "unknown" understates spend.
func TestCrossProviderNormalisation(t *testing.T) {
	c := New(table(), nil)
	for _, tc := range []struct{ provider, model string }{
		{"github", "claude-opus-4.6"},
		{"opencode", "claude-opus-4-6"},
	} {
		got := c.Resolve(eventschema.Provider(tc.provider), tc.model)
		if got.Basis != BasisNormalised {
			t.Errorf("%s/%s Basis = %q, want %q", tc.provider, tc.model, got.Basis, BasisNormalised)
		}
		if got.CostPerMillion != 30 {
			t.Errorf("%s/%s cost = %v, want 30 (5+25 from anthropic)", tc.provider, tc.model, got.CostPerMillion)
		}
	}
}

// Price rank inside a provider is the tier signal, because it is the one
// that updates itself: the rate card refreshes daily, while a hardcoded
// list of model names rots every release.
func TestTierFromPriceRank(t *testing.T) {
	c := New(table(), nil)
	for _, tc := range []struct {
		model string
		want  Tier
	}{
		{"claude-haiku-4-5", TierLookup},
		{"claude-sonnet-5", TierBalanced},
		{"claude-opus-5", TierDefault},
		{"claude-fable-5", TierDeep},
	} {
		if got := c.Resolve("anthropic", tc.model); got.Tier != tc.want {
			t.Errorf("Resolve(anthropic/%s).Tier = %q, want %q (cost %v)",
				tc.model, got.Tier, tc.want, got.CostPerMillion)
		}
	}
}

// A model the card has never heard of still has a readable name. This is
// a weaker signal than price and is marked as such, so a caller can
// decide whether to act on it.
func TestFamilyHeuristicWhenUnpriced(t *testing.T) {
	c := New(table(), nil)
	if got := c.Resolve("anthropic", "claude-fable-5-1"); got.Tier != TierDeep {
		t.Errorf("fable-5-1 tier = %q, want deep (card lags the vendor)", got.Tier)
	}
	if got := c.Resolve("google", "gemini-3-flash"); got.Tier != TierLookup {
		t.Errorf("gemini-3-flash tier = %q, want lookup", got.Tier)
	}
	if got := c.Resolve("anthropic", "claude-fable-5-1"); got.Basis != BasisFamily {
		t.Errorf("Basis = %q, want %q", got.Basis, BasisFamily)
	}
}

// Guessing is worse than abstaining: routing real work onto a model
// nobody can price, on the strength of a name nobody recognises, is the
// trade an operator did not ask for.
func TestAbstainsOnGenuinelyUnknownModels(t *testing.T) {
	c := New(table(), nil)
	for _, m := range []string{"big-pickle", "kimi-k2.5", "glm-5"} {
		got := c.Resolve("opencode", m)
		if got.Tier != TierUnknown {
			t.Errorf("Resolve(opencode/%s).Tier = %q, want unknown", m, got.Tier)
		}
	}
}

// An operator who knows what a model is must be able to say so.
func TestOverrideWins(t *testing.T) {
	c := New(table(), map[string]Tier{"opencode/glm-5": TierBalanced})
	got := c.Resolve("opencode", "glm-5")
	if got.Tier != TierBalanced || got.Basis != BasisOverride {
		t.Errorf("override = %+v, want balanced/override", got)
	}
}

// Target answers the routing question: given a tier, what should this
// provider actually run? Free models win ties because they cost nothing.
func TestTargetPicksForTier(t *testing.T) {
	c := New(table(), nil)
	if got, ok := c.Target("anthropic", TierLookup); !ok || got != "claude-haiku-4-5" {
		t.Errorf("Target(anthropic, lookup) = %q,%v want claude-haiku-4-5", got, ok)
	}
	if got, ok := c.Target("anthropic", TierBalanced); !ok || got != "claude-sonnet-5" {
		t.Errorf("Target(anthropic, balanced) = %q,%v want claude-sonnet-5", got, ok)
	}
	if _, ok := c.Target("nosuchprovider", TierLookup); ok {
		t.Error("Target on an unknown provider should not invent one")
	}
}

// The rate card keeps retired models, and they sit at both ends of the
// price range: a long-gone Haiku below the current cheap model and a
// superseded Opus above the current flagship. Ranking across all of them
// pushes the live generation into the middle — the cheapest model an
// operator can actually pick comes back "balanced" and the most capable
// comes back "default", which is exactly backwards for routing.
//
// Tiers are therefore ranked over the models the operator can actually
// choose, when a caller knows what those are.
func TestRetiredModelsDoNotSkewTiers(t *testing.T) {
	full := spend.Table{Currency: "USD", Rates: map[spend.Key]spend.Rate{
		{Provider: "anthropic", Model: "claude-3-haiku"}:    {InputPerMillion: 0.25, OutputPerMillion: 1.25},
		{Provider: "anthropic", Model: "claude-haiku-4-5"}:  {InputPerMillion: 1, OutputPerMillion: 5},
		{Provider: "anthropic", Model: "claude-sonnet-5"}:   {InputPerMillion: 2, OutputPerMillion: 10},
		{Provider: "anthropic", Model: "claude-sonnet-4-6"}: {InputPerMillion: 3, OutputPerMillion: 15},
		{Provider: "anthropic", Model: "claude-opus-5"}:     {InputPerMillion: 5, OutputPerMillion: 25},
		{Provider: "anthropic", Model: "claude-fable-5-1"}:  {InputPerMillion: 10, OutputPerMillion: 50},
		{Provider: "anthropic", Model: "claude-opus-4-1"}:   {InputPerMillion: 15, OutputPerMillion: 75},
	}}
	c := New(full, nil).WithCandidates([]string{
		"claude-haiku-4-5", "claude-sonnet-5", "claude-opus-5", "claude-fable-5-1",
	})
	for _, tc := range []struct {
		model string
		want  Tier
	}{
		{"claude-haiku-4-5", TierLookup},
		{"claude-sonnet-5", TierBalanced},
		{"claude-opus-5", TierDefault},
		{"claude-fable-5-1", TierDeep},
	} {
		if got := c.Resolve("anthropic", tc.model); got.Tier != tc.want {
			t.Errorf("Resolve(anthropic/%s).Tier = %q, want %q", tc.model, got.Tier, tc.want)
		}
	}
	// And the target for the cheap tier is the live model, not the
	// retired one that happens to be cheaper.
	if got, ok := c.Target("anthropic", TierLookup); !ok || got != "claude-haiku-4-5" {
		t.Errorf("Target(lookup) = %q,%v want claude-haiku-4-5", got, ok)
	}
}

// Snapshot rows are filed under a prefix pattern ("claude-sonnet-5*").
// A caller routing a turn needs a model name it can send, so the marker
// must not leak out of Target.
func TestTargetReturnsASendableModelName(t *testing.T) {
	c := New(spend.Table{Currency: "USD", Rates: map[spend.Key]spend.Rate{
		{Provider: "anthropic", Model: "claude-haiku-4-5*"}: {InputPerMillion: 1, OutputPerMillion: 5},
		{Provider: "anthropic", Model: "claude-opus-5*"}:    {InputPerMillion: 5, OutputPerMillion: 25},
	}}, nil)
	got, ok := c.Target("anthropic", TierLookup)
	if !ok {
		t.Fatal("no target")
	}
	if strings.HasSuffix(got, "*") {
		t.Errorf("Target = %q, must not carry the prefix marker", got)
	}
	if got != "claude-haiku-4-5" {
		t.Errorf("Target = %q, want claude-haiku-4-5", got)
	}
}

// The card files a rate under a prefix pattern so version-suffixed
// models resolve to their family rate, which means a candidate named
// exactly ("claude-fable-5-1") and the row that prices it
// ("claude-fable-5*") are not the same string. Matching them only by
// equality drops the model from tier ranking, and the flagship's absence
// promotes whatever is left into the top tier.
func TestCandidatesMatchPrefixKeys(t *testing.T) {
	c := New(spend.Table{Currency: "USD", Rates: map[spend.Key]spend.Rate{
		{Provider: "anthropic", Model: "claude-haiku-4-5*"}: {InputPerMillion: 1, OutputPerMillion: 5},
		{Provider: "anthropic", Model: "claude-sonnet-5*"}:  {InputPerMillion: 2, OutputPerMillion: 10},
		{Provider: "anthropic", Model: "claude-opus-5*"}:    {InputPerMillion: 5, OutputPerMillion: 25},
		{Provider: "anthropic", Model: "claude-fable-5*"}:   {InputPerMillion: 10, OutputPerMillion: 50},
	}}, nil).WithCandidates([]string{
		"claude-haiku-4-5", "claude-sonnet-5", "claude-opus-5", "claude-fable-5-1",
	})
	if got := c.Resolve("anthropic", "claude-opus-5"); got.Tier != TierDefault {
		t.Errorf("opus-5 tier = %q, want default (fable must rank above it)", got.Tier)
	}
	if got := c.Resolve("anthropic", "claude-fable-5-1"); got.Tier != TierDeep {
		t.Errorf("fable-5-1 tier = %q, want deep", got.Tier)
	}
}

// A proxy provider re-badges someone else's models: opencode and Copilot
// both serve Anthropic models under their own name, and the rate card
// has no rows filed under theirs at all. Resolution already handles that
// by matching the model name across providers — but a target search that
// scans the card for rows belonging to the requesting provider finds
// nothing and silently gives up, so the client that most needs routing
// gets none.
//
// The menu is the candidate list, which is what the operator declared.
func TestTargetWorksForProxyProviders(t *testing.T) {
	c := New(spend.Table{Currency: "USD", Rates: map[spend.Key]spend.Rate{
		{Provider: "anthropic", Model: "claude-sonnet-5*"}: {InputPerMillion: 2, OutputPerMillion: 10},
		{Provider: "anthropic", Model: "claude-opus-4-6*"}: {InputPerMillion: 5, OutputPerMillion: 25},
	}}, nil).WithCandidates([]string{"glm-5-free", "claude-sonnet-5", "claude-opus-4-6"})

	// The free model costs nothing and is the right answer for retrieval.
	if got, ok := c.Target("opencode", TierLookup); !ok || got != "glm-5-free" {
		t.Errorf("Target(opencode, lookup) = %q,%v want glm-5-free", got, ok)
	}
	// A three-model menu holds only two priced models, so it cannot
	// express four bands and the balanced tier is genuinely empty.
	// TargetBelow answers the question that still has an answer: the
	// most capable model that is cheaper than what the turn is on.
	if got, ok := c.TargetBelow("opencode", TierDeep); !ok || got != "claude-sonnet-5" {
		t.Errorf("TargetBelow(opencode, deep) = %q,%v want claude-sonnet-5", got, ok)
	}
}

// Candidates are indexed by a normalised name so they can be matched
// against card rows, but the name handed back has to be the one the
// operator wrote: "gpt-5.3-codex" normalises to "gpt-5-3-codex", and
// nothing will answer to that. Anthropic names carry no dots, which is
// why this stayed invisible until a second provider was wired.
func TestTargetReturnsTheOperatorsSpelling(t *testing.T) {
	c := New(spend.Table{Currency: "USD", Rates: map[spend.Key]spend.Rate{
		{Provider: "openai", Model: "gpt-5.6-luna*"}:  {InputPerMillion: 0.4, OutputPerMillion: 1},
		{Provider: "openai", Model: "gpt-5-mini*"}:    {InputPerMillion: 0.25, OutputPerMillion: 2},
		{Provider: "openai", Model: "gpt-5.3-codex*"}: {InputPerMillion: 1.75, OutputPerMillion: 14},
		{Provider: "openai", Model: "gpt-5.5*"}:       {InputPerMillion: 10, OutputPerMillion: 25},
	}}, nil).WithCandidates([]string{"gpt-5.6-luna", "gpt-5-mini", "gpt-5.3-codex", "gpt-5.5"})

	got, ok := c.Target("openai", TierLookup)
	if !ok {
		t.Fatal("no lookup target")
	}
	if got != "gpt-5.6-luna" {
		t.Errorf("Target(openai, lookup) = %q, want gpt-5.6-luna (cheapest, and spelled as configured)", got)
	}
	for _, tr := range []Tier{TierLookup, TierBalanced, TierDefault, TierDeep} {
		if m, ok := c.Target("openai", tr); ok && strings.Contains(m, "gpt-5-3") {
			t.Errorf("Target(%s) returned a normalised name %q", tr, m)
		}
	}
}
