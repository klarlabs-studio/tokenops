package spend

import (
	"testing"

	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// Rates hand-checked against
// developers.openai.com/api/docs/models/gpt-6-astra on 2026-09-16 and
// cross-checked against OpenAI's own launch announcement. Both agreed:
// $10 input, $1 cached input, $12.50 cache writes, $50 output.
//
// Locked down here for the same reason the Anthropic rates are — the
// consistency guard does not cover non-Anthropic providers, so pinning
// is the only protection against an upstream source going stale.
func TestVerifiedGPT6AstraRates(t *testing.T) {
	r, err := DefaultTable().Lookup(eventschema.ProviderOpenAI, "gpt-6-astra")
	if err != nil {
		t.Fatalf("gpt-6-astra not priced: %v", err)
	}
	for _, tc := range []struct {
		name      string
		got, want float64
	}{
		{"input", r.InputPerMillion, 10.00},
		{"output", r.OutputPerMillion, 50.00},
		{"cached input", r.CachedInputPerMillion, 1.00},
	} {
		if tc.got != tc.want {
			t.Errorf("%s = %v, want %v", tc.name, tc.got, tc.want)
		}
	}
}

// The key is exact on purpose. OpenAI also ships "GPT-6 Astra Pro" and
// its rate is not published in this catalog; a prefix key would price it
// as standard Astra, which is confidently wrong. Unpriced is the honest
// direction to fail in — and as of today it is reported rather than
// silently costed at zero.
func TestAstraKeyDoesNotSwallowAstraPro(t *testing.T) {
	if _, err := DefaultTable().Lookup(eventschema.ProviderOpenAI, "gpt-6-astra-pro"); err == nil {
		t.Error("gpt-6-astra-pro was priced from the standard Astra row")
	}
}

func TestVerifiedGPT6SolAndLunaRates(t *testing.T) {
	tests := map[string]Rate{
		"gpt-6-sol":  {InputPerMillion: 2.00, OutputPerMillion: 10.00, CachedInputPerMillion: 0.20},
		"gpt-6-luna": {InputPerMillion: 0.10, OutputPerMillion: 0.50, CachedInputPerMillion: 0.01},
	}
	for model, want := range tests {
		t.Run(model, func(t *testing.T) {
			got, err := DefaultTable().Lookup(eventschema.ProviderOpenAI, model)
			if err != nil {
				t.Fatalf("%s not priced: %v", model, err)
			}
			if got.InputPerMillion != want.InputPerMillion ||
				got.OutputPerMillion != want.OutputPerMillion ||
				got.CachedInputPerMillion != want.CachedInputPerMillion {
				t.Errorf("%s rate = %+v, want %+v", model, got, want)
			}
		})
	}
}

func TestGPT6SolAndLunaKeysAreExact(t *testing.T) {
	for _, model := range []string{"gpt-6-sol-pro", "gpt-6-luna-preview"} {
		if _, err := DefaultTable().Lookup(eventschema.ProviderOpenAI, model); err == nil {
			t.Errorf("%s was priced from an exact GPT-6 row", model)
		}
	}
}

// The models Codex runs on, from OpenAI's Standard table (2026-10-04).
// Before these rows, 80% of a Codex user's requests were unpriced.
func TestVerifiedCodexModelRates(t *testing.T) {
	tests := map[string]Rate{
		"gpt-6.1-sol":   {InputPerMillion: 2.00, OutputPerMillion: 10.00, CachedInputPerMillion: 0.10},
		"gpt-5.6-sol":   {InputPerMillion: 4.00, OutputPerMillion: 20.00, CachedInputPerMillion: 0.40},
		"gpt-5.6-terra": {InputPerMillion: 2.00, OutputPerMillion: 12.00, CachedInputPerMillion: 0.20},
		"gpt-5.6-luna":  {InputPerMillion: 0.20, OutputPerMillion: 1.20, CachedInputPerMillion: 0.02},
		"gpt-5.5":       {InputPerMillion: 5.00, OutputPerMillion: 30.00, CachedInputPerMillion: 0.50},
		"gpt-5.5-pro":   {InputPerMillion: 30.00, OutputPerMillion: 180.00},
	}
	for model, want := range tests {
		got, err := DefaultTable().Lookup(eventschema.ProviderOpenAI, model)
		if err != nil {
			t.Errorf("%s not priced: %v", model, err)
			continue
		}
		if got.InputPerMillion != want.InputPerMillion || got.OutputPerMillion != want.OutputPerMillion ||
			got.CachedInputPerMillion != want.CachedInputPerMillion {
			t.Errorf("%s rate = %+v, want %+v", model, got, want)
		}
	}
	// Siblings at other rates stay unpriced rather than borrow a row.
	for _, model := range []string{"gpt-5.6-cyber", "gpt-5.5-cyber", "gpt-6.1-sol-pro", "codex-auto-review"} {
		if _, err := DefaultTable().Lookup(eventschema.ProviderOpenAI, model); err == nil {
			t.Errorf("%s was priced from another model's row", model)
		}
	}
}
