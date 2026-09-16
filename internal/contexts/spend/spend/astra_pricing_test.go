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
