package pricing

import (
	"testing"
	"time"
)

func TestBaselineSnapshot_AnthropicScopedAndPopulated(t *testing.T) {
	base := BaselineSnapshot()
	if base.Source != SourceEmbeddedBaseline {
		t.Fatalf("source = %q, want %q", base.Source, SourceEmbeddedBaseline)
	}
	if base.FetchedAt.IsZero() {
		t.Error("baseline must carry a fixed FetchedAt")
	}
	// Keys must be normalized (no trailing "*") and include the Opus family.
	if _, ok := base.Rates["claude-opus-4-8"]; !ok {
		t.Fatalf("baseline missing claude-opus-4-8; keys=%v", base.Models())
	}
	for k := range base.Rates {
		if k == "" || k[len(k)-1] == '*' {
			t.Errorf("baseline key %q not normalized", k)
		}
	}
	// Opus baseline must be the corrected rate, and internally consistent.
	opus := base.Rates["claude-opus-4-8"]
	if opus.InputPerMillion != 15 || opus.OutputPerMillion != 75 || opus.CachedInputPerMillion != 1.5 {
		t.Errorf("opus baseline = %+v, want 15/75/1.5", opus)
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
