package pricing

import (
	"testing"
	"time"
)

func TestBaselineSnapshot_AllProvidersPopulated(t *testing.T) {
	base := BaselineSnapshot()
	if base.Source != SourceEmbeddedBaseline {
		t.Fatalf("source = %q, want %q", base.Source, SourceEmbeddedBaseline)
	}
	if base.FetchedAt.IsZero() {
		t.Error("baseline must carry a fixed FetchedAt")
	}
	// Keys are "<provider>/<model>", normalized (no trailing "*"), and span
	// multiple providers — not just Anthropic.
	for _, want := range []string{
		"anthropic/claude-opus-4-8",
		"openai/gpt-4o",
		"mistral/mistral-large",
		"gemini/gemini-2.5-pro",
	} {
		if _, ok := base.Rates[want]; !ok {
			t.Errorf("baseline missing %q; keys=%v", want, base.Models())
		}
	}
	providers := map[string]bool{}
	for k := range base.Rates {
		provider, model := splitSnapKey(k)
		if provider == "" || model == "" || model[len(model)-1] == '*' {
			t.Errorf("baseline key %q not a normalized provider/model", k)
		}
		providers[provider] = true
	}
	if len(providers) < 3 {
		t.Errorf("baseline should span many providers, got %d: %v", len(providers), providers)
	}
	// Opus baseline must be the corrected rate, and internally consistent.
	opus := base.Rates["anthropic/claude-opus-4-8"]
	if opus.InputPerMillion != 5 || opus.OutputPerMillion != 25 || opus.CachedInputPerMillion != 0.5 {
		t.Errorf("opus baseline = %+v, want 5/25/0.5", opus)
	}
}

func TestBaselineSnapshot_Deterministic(t *testing.T) {
	a := BaselineSnapshot()
	b := BaselineSnapshot()
	if !a.FetchedAt.Equal(b.FetchedAt) {
		t.Error("baseline FetchedAt must be stable across calls")
	}
}

func TestRateConversionRoundTrip(t *testing.T) {
	r := Rate{InputPerMillion: 3, OutputPerMillion: 15, CachedInputPerMillion: 0.3}
	if got := FromSpendRate(r.ToSpendRate()); got != r {
		t.Errorf("round trip = %+v, want %+v", got, r)
	}
}

func TestBaselineFetchedAtFixed(t *testing.T) {
	want := time.Date(2026, 7, 7, 0, 0, 0, 0, time.UTC)
	if !BaselineSnapshot().FetchedAt.Equal(want) {
		t.Errorf("baseline FetchedAt = %s, want %s", BaselineSnapshot().FetchedAt, want)
	}
}
