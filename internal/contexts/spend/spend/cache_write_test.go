package spend

import (
	"testing"

	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// Anthropic bills a prompt-cache write above the plain input rate: 1.25×
// for a five-minute entry, 2× for a one-hour one. Pricing writes as plain
// input under-reports every cache-heavy Claude session.
func TestComputePricesCacheWritesAtTheirRate(t *testing.T) {
	rate := Rate{
		InputPerMillion:        4,
		OutputPerMillion:       20,
		CachedInputPerMillion:  0.2,
		CacheWritePerMillion:   5,
		CacheWrite1hPerMillion: 8,
	}
	key := Key{Provider: eventschema.ProviderAnthropic, Model: "claude-test"}
	withRate := NewEngine(Table{Rates: map[Key]Rate{key: rate}})
	noWriteRate := NewEngine(Table{Rates: map[Key]Rate{key: {InputPerMillion: 4, OutputPerMillion: 20}}})

	for _, tc := range []struct {
		name   string
		engine *Engine
		event  eventschema.PromptEvent
		want   float64
	}{
		{
			name:   "uncached, read, 5m write and 1h write each at their rate",
			engine: withRate,
			// 4M input = 1M uncached + 1M read + 1M 5m write + 1M 1h write.
			event: eventschema.PromptEvent{
				InputTokens: 4_000_000, CachedInputTokens: 1_000_000,
				CacheWriteInputTokens: 2_000_000, CacheWrite1hInputTokens: 1_000_000,
			},
			want: 4 + 0.2 + 5 + 8,
		},
		{
			name:   "1h writes fall back to the 5m write rate when unpriced",
			engine: NewEngine(Table{Rates: map[Key]Rate{key: {InputPerMillion: 4, CacheWritePerMillion: 5}}}),
			event:  eventschema.PromptEvent{InputTokens: 1_000_000, CacheWriteInputTokens: 1_000_000, CacheWrite1hInputTokens: 1_000_000},
			want:   5,
		},
		{
			name:   "writes fall back to the input rate when the card has none",
			engine: noWriteRate,
			event:  eventschema.PromptEvent{InputTokens: 2_000_000, CacheWriteInputTokens: 1_000_000},
			want:   8,
		},
		{
			name:   "reported writes never exceed the input they are part of",
			engine: withRate,
			event: eventschema.PromptEvent{
				InputTokens: 1_000_000, CachedInputTokens: 600_000, CacheWriteInputTokens: 900_000,
			},
			want: 0.6*0.2 + 0.4*5,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := tc.event
			p.Provider, p.RequestModel = key.Provider, key.Model
			got, err := tc.engine.Compute(&p)
			if err != nil {
				t.Fatalf("compute: %v", err)
			}
			if !approxEqual(got, tc.want) {
				t.Errorf("cost = %.6f, want %.6f", got, tc.want)
			}
		})
	}
}

// The embedded card carries Anthropic's documented write multipliers, so a
// fresh install prices writes correctly without a refresh.
func TestDefaultTablePricesAnthropicCacheWrites(t *testing.T) {
	for key, r := range DefaultTable().Rates {
		if key.Provider != eventschema.ProviderAnthropic {
			continue
		}
		if !approxEqual(r.CacheWritePerMillion, r.InputPerMillion*1.25) ||
			!approxEqual(r.CacheWrite1hPerMillion, r.InputPerMillion*2) {
			t.Errorf("%s: write %v / 1h %v, want 1.25× and 2× of input %v",
				key.Model, r.CacheWritePerMillion, r.CacheWrite1hPerMillion, r.InputPerMillion)
		}
	}
}
