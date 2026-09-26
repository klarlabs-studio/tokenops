package spend

import (
	"testing"

	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// Rates hand-checked against Anthropic's model-specific pages and pricing page
// on 2026-09-26. Locked down here so an upstream source that goes stale cannot
// silently regress a model the catalog claims to have verified.
func TestVerifiedAnthropicRates(t *testing.T) {
	table := DefaultTable()
	for _, tc := range []struct {
		model                  string
		in, out, cachedPerMTok float64
	}{
		{"claude-opus-5-5", 4.00, 20.00, 0.20},
		{"claude-opus-5-5[1m]", 4.00, 20.00, 0.20},
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

// Anthropic's current rates use a 5% cache-read rate for Opus 5.5 and 10% for
// the other listed models. The consistency guard allows this documented
// model-specific exception.
func TestAnthropicCacheReadRatioHolds(t *testing.T) {
	table := DefaultTable()
	for _, tc := range []struct {
		model      string
		cacheRatio float64
	}{
		{"claude-opus-5-5", 0.05},
		{"claude-opus-5", 0.10},
		{"claude-fable-5", 0.10},
		{"claude-mythos-5", 0.10},
		{"claude-sonnet-5", 0.10},
		{"claude-haiku-4-5", 0.10},
	} {
		rate, err := table.Lookup(eventschema.ProviderAnthropic, tc.model)
		if err != nil {
			t.Fatalf("%s: %v", tc.model, err)
		}
		want := rate.InputPerMillion * tc.cacheRatio
		if diff := rate.CachedInputPerMillion - want; diff > 0.001 || diff < -0.001 {
			t.Errorf("%s cache-read = %.3f, want ~%.3f (%.2fx input)",
				tc.model, rate.CachedInputPerMillion, want, tc.cacheRatio)
		}
		wantOut := rate.InputPerMillion * 5
		if diff := rate.OutputPerMillion - wantOut; diff > 0.001 || diff < -0.001 {
			t.Errorf("%s output = %.2f, want ~%.2f (5x input)",
				tc.model, rate.OutputPerMillion, wantOut)
		}
	}
}
