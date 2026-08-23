package spend

import (
	"testing"

	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// Rates hand-checked against platform.claude.com/docs/en/about-claude/pricing
// on 2026-08-23 and cross-checked against the LiteLLM snapshot. Both agreed.
// Locked down here so an upstream source that goes stale cannot silently
// regress a model the catalog claims to have verified.
func TestVerifiedAnthropicRates(t *testing.T) {
	table := DefaultTable()
	for _, tc := range []struct {
		model                  string
		in, out, cachedPerMTok float64
	}{
		{"claude-opus-5", 5.00, 25.00, 0.50},
		{"claude-opus-5[1m]", 5.00, 25.00, 0.50},
		{"claude-opus-4-8", 5.00, 25.00, 0.50},
		{"claude-fable-5", 10.00, 50.00, 1.00},
		{"claude-mythos-5", 10.00, 50.00, 1.00},
		{"claude-sonnet-5", 2.00, 10.00, 0.20},
		{"claude-haiku-4-5", 1.00, 5.00, 0.10},
	} {
		rate, err := table.Lookup(eventschema.ProviderAnthropic, tc.model)
		if err != nil {
			t.Errorf("%s: unpriced (%v)", tc.model, err)
			continue
		}
		if rate.InputPerMillion != tc.in {
			t.Errorf("%s input = %.2f, want %.2f", tc.model, rate.InputPerMillion, tc.in)
		}
		if rate.OutputPerMillion != tc.out {
			t.Errorf("%s output = %.2f, want %.2f", tc.model, rate.OutputPerMillion, tc.out)
		}
		if rate.CachedInputPerMillion != tc.cachedPerMTok {
			t.Errorf("%s cache-read = %.2f, want %.2f",
				tc.model, rate.CachedInputPerMillion, tc.cachedPerMTok)
		}
	}
}

// The vendor prices a cache hit at 0.1x base input across the family. The
// pricing consistency guard enforces this ratio, so a row that drifts off
// it is a data-entry error rather than a real rate change.
func TestAnthropicCacheReadRatioHolds(t *testing.T) {
	table := DefaultTable()
	for _, model := range []string{
		"claude-opus-5", "claude-fable-5", "claude-mythos-5",
		"claude-sonnet-5", "claude-haiku-4-5",
	} {
		rate, err := table.Lookup(eventschema.ProviderAnthropic, model)
		if err != nil {
			t.Fatalf("%s: %v", model, err)
		}
		want := rate.InputPerMillion * 0.1
		if diff := rate.CachedInputPerMillion - want; diff > 0.001 || diff < -0.001 {
			t.Errorf("%s cache-read = %.3f, want ~%.3f (0.1x input)",
				model, rate.CachedInputPerMillion, want)
		}
		wantOut := rate.InputPerMillion * 5
		if diff := rate.OutputPerMillion - wantOut; diff > 0.001 || diff < -0.001 {
			t.Errorf("%s output = %.2f, want ~%.2f (5x input)",
				model, rate.OutputPerMillion, wantOut)
		}
	}
}
