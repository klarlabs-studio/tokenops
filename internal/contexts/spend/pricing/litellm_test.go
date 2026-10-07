package pricing

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// fixtureURL is the provenance the fixture snapshot records.
const fixtureURL = "https://fixture.invalid/litellm.json"

// parseFixture normalizes the trimmed LiteLLM sample from testdata. The HTTP
// fetch is internal/infra/pricingsource's and is tested there.
func parseFixture(t *testing.T) Snapshot {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("testdata", "litellm_sample.json"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	snap, err := ParseLiteLLM(body, fixtureURL, time.Now().UTC())
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return snap
}

func TestParseLiteLLM_MapsEveryCatalogProvider(t *testing.T) {
	snap := parseFixture(t)
	if snap.Source != "litellm" || snap.SourceURL != fixtureURL {
		t.Errorf("provenance = %q/%q", snap.Source, snap.SourceURL)
	}
	// Anthropic, OpenAI, and Mistral entries all map to their tokenops
	// provider under a "<provider>/<model>" key.
	for _, want := range []string{
		"anthropic/claude-opus-4-8",
		"openai/gpt-4o",
		"mistral/mistral-large",
	} {
		if _, ok := snap.Rates[want]; !ok {
			t.Errorf("missing %q; keys=%v", want, snap.Models())
		}
	}
	// The sample_spec doc row and an unmapped provider (fireworks_ai) are
	// filtered out so the key-space matches the baseline.
	if _, ok := snap.Rates["sample_spec"]; ok {
		t.Error("sample_spec doc row leaked into snapshot")
	}
	for k := range snap.Rates {
		if provider, _ := splitSnapKey(k); provider == "fireworks_ai" || provider == "" {
			t.Errorf("unmapped/unqualified provider leaked: %q", k)
		}
	}
}

func TestParseLiteLLM_PerTokenToPerMillion(t *testing.T) {
	snap := parseFixture(t)
	opus, ok := snap.Rates["anthropic/claude-opus-4-8"]
	if !ok {
		t.Fatalf("opus not mapped; keys=%v", snap.Models())
	}
	// 0.000005 per token → 5 per million.
	if opus.InputPerMillion != 5 || opus.OutputPerMillion != 25 || opus.CachedInputPerMillion != 0.5 {
		t.Errorf("opus per-million = %+v, want 5/25/0.5", opus)
	}
	// OpenAI: 0.0000025 → 2.5 per million.
	gpt, ok := snap.Rates["openai/gpt-4o"]
	if !ok {
		t.Fatalf("gpt-4o not mapped; keys=%v", snap.Models())
	}
	if gpt.InputPerMillion != 2.5 || gpt.OutputPerMillion != 10 || gpt.CachedInputPerMillion != 1.25 {
		t.Errorf("gpt-4o per-million = %+v, want 2.5/10/1.25", gpt)
	}
	// Mistral: 0.0000005 → 0.5 per million, no cache.
	mistral, ok := snap.Rates["mistral/mistral-large"]
	if !ok {
		t.Fatalf("mistral-large not mapped; keys=%v", snap.Models())
	}
	if mistral.InputPerMillion != 0.5 || mistral.OutputPerMillion != 1.5 {
		t.Errorf("mistral-large per-million = %+v, want 0.5/1.5", mistral)
	}
}

func TestParseLiteLLM_KeyMapping(t *testing.T) {
	snap := parseFixture(t)
	// Dated ids collapse to the catalog prefix key (provider-qualified).
	if _, ok := snap.Rates["anthropic/claude-3-5-sonnet"]; !ok {
		t.Errorf("dated sonnet not mapped to catalog key; keys=%v", snap.Models())
	}
	if _, ok := snap.Rates["anthropic/claude-3-5-haiku"]; !ok {
		t.Errorf("dated haiku not mapped to catalog key; keys=%v", snap.Models())
	}
	// A "<vendor>/<model>-latest" id strips both prefix and suffix.
	if _, ok := snap.Rates["mistral/mistral-large"]; !ok {
		t.Errorf("mistral-large-latest not normalized; keys=%v", snap.Models())
	}
	// A model with no catalog key surfaces under its normalized id (date stripped).
	if _, ok := snap.Rates["anthropic/claude-3-opus"]; !ok {
		t.Errorf("new model should surface under normalized key; keys=%v", snap.Models())
	}
}

func TestPreferID_CurrentPriceWins(t *testing.T) {
	cases := []struct {
		name, a, b string
	}{
		{"newer date beats older", "mistral-large-2411", "mistral-large-2402"},
		{"dated beats latest (stale alias)", "codestral-2508", "codestral-latest"},
		{"dated beats bare", "mistral-medium-2505", "mistral-medium"},
		{"latest beats bare", "mistral-medium-latest", "mistral-medium"},
		{"8-digit newer beats older", "claude-3-5-sonnet-20241022", "claude-3-5-sonnet-20240620"},
		{"vendor prefix ignored", "mistral/mistral-large-2411", "mistral/mistral-large-2402"},
		// OpenAI MMDD (month-first, year implied) must NOT be read as a date:
		// else -1106 (tier 3, score 1106) would beat the bare alias. Rejecting
		// it drops -1106 to the bare tier, where the shorter alias wins lexically.
		{"MMDD not a date; bare alias wins", "gpt-3.5-turbo", "gpt-3.5-turbo-1106"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if !preferID(c.a, c.b) {
				t.Errorf("preferID(%q, %q) = false, want true", c.a, c.b)
			}
			if preferID(c.b, c.a) {
				t.Errorf("preferID is not antisymmetric for %q vs %q", c.a, c.b)
			}
		})
	}
}

// When several dated SKUs collapse onto one catalog key, the fetched rate must
// be the newest dated snapshot, not the oldest archived one nor a stale
// "-latest" alias. The fixture's mistral-large-2402 ($4/$12) and -latest ($3/$9)
// must lose to the newest dated -2411 ($0.50/$1.50, the current Large 3 rate).
func TestParseLiteLLM_CollisionAdoptsCurrentRate(t *testing.T) {
	snap := parseFixture(t)
	large, ok := snap.Rates["mistral/mistral-large"]
	if !ok {
		t.Fatalf("mistral-large not mapped; keys=%v", snap.Models())
	}
	if large.InputPerMillion != 0.5 || large.OutputPerMillion != 1.5 {
		t.Errorf("mistral-large = %+v, want the newest-dated rate 0.5/1.5 (not an archived SKU)", large)
	}
}

// OpenAI's MMDD snapshots (gpt-3.5-turbo-1106 = Nov, -0125 = Jan) must not be
// treated as YYMM dates — 1106 > 0125 numerically would pick the older, pricier
// November SKU. The bare "gpt-3.5-turbo" alias ($0.50/$1.50) must win instead.
func TestParseLiteLLM_MMDDNotMisorderedAsDate(t *testing.T) {
	snap := parseFixture(t)
	gpt, ok := snap.Rates["openai/gpt-3.5-turbo"]
	if !ok {
		t.Fatalf("gpt-3.5-turbo not mapped; keys=%v", snap.Models())
	}
	if gpt.InputPerMillion != 0.5 || gpt.OutputPerMillion != 1.5 {
		t.Errorf("gpt-3.5-turbo = %+v, want the bare-alias rate 0.5/1.5 (not the -1106 MMDD SKU)", gpt)
	}
}

// A broad catalog key must not absorb a distinct SKU tier: grok-3-fast ($5/$25)
// and grok-3-mini ($0.30/$0.50) must not fold into grok-3 ($3/$15), or the base
// model's fetched rate would be a different product's price.
func TestParseLiteLLM_DistinctSKUTierNotFolded(t *testing.T) {
	snap := parseFixture(t)
	base, ok := snap.Rates["xai/grok-3"]
	if !ok {
		t.Fatalf("grok-3 not mapped; keys=%v", snap.Models())
	}
	if base.InputPerMillion != 3 || base.OutputPerMillion != 15 {
		t.Errorf("grok-3 = %+v, want its own 3/15 rate (not grok-3-fast's 5/25)", base)
	}
	// The distinct tiers surface under their own keys, not folded into grok-3.
	for _, want := range []string{"xai/grok-3-fast", "xai/grok-3-mini"} {
		if _, ok := snap.Rates[want]; !ok {
			t.Errorf("expected distinct SKU %q to surface separately; keys=%v", want, snap.Models())
		}
	}
}

func TestParseLiteLLM_FetchedSnapshotPassesGuard(t *testing.T) {
	snap := parseFixture(t)
	if a := Check(snap); len(a) != 0 {
		t.Errorf("clean fixture flagged by guard: %v", a)
	}
}

func TestParseLiteLLM_DiffVsBaselineIsClean(t *testing.T) {
	snap := parseFixture(t)
	// The fixture's overlapping models match the corrected baseline, so the
	// only diffs should be additions (claude-3-opus), never a modification of
	// an existing rate.
	for _, c := range Diff(BaselineSnapshot(), snap) {
		if c.Kind == ChangeModified {
			t.Errorf("unexpected rate change vs baseline: %s", FormatChange(c))
		}
	}
}

func TestParseLiteLLM_BadJSONWrapped(t *testing.T) {
	if _, err := ParseLiteLLM([]byte("not json"), fixtureURL, time.Now()); !errors.Is(err, ErrFetch) {
		t.Errorf("bad JSON should wrap ErrFetch, got %v", err)
	}
}
