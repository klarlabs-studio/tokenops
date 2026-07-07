package pricing

import (
	"strings"
	"testing"
	"time"
)

func guardSnap(rates map[string]Rate) Snapshot {
	return Snapshot{Source: "test", FetchedAt: time.Unix(0, 0), Rates: rates}
}

func TestCheck_BaselinePassesGuard(t *testing.T) {
	// The corrected baseline must be clean — this is the regression guard for
	// the Opus ⅓ error ever reappearing in the embedded catalog.
	if anomalies := Check(BaselineSnapshot()); len(anomalies) > 0 {
		for _, a := range anomalies {
			t.Errorf("baseline anomaly: %s", a)
		}
	}
}

func TestCheck_CleanFamilyPassesQuiet(t *testing.T) {
	s := guardSnap(map[string]Rate{
		"anthropic/claude-opus-4-8":   {InputPerMillion: 15, OutputPerMillion: 75, CachedInputPerMillion: 1.5},
		"anthropic/claude-sonnet-4-6": {InputPerMillion: 3, OutputPerMillion: 15, CachedInputPerMillion: 0.3},
		"anthropic/claude-haiku-4-5":  {InputPerMillion: 1, OutputPerMillion: 5, CachedInputPerMillion: 0.1},
	})
	if a := Check(s); len(a) != 0 {
		t.Errorf("clean family flagged: %v", a)
	}
}

func TestCheck_CatchesOpusThirdError(t *testing.T) {
	// The historical bug: input entered at ⅓ ($5) while cache-read stayed at the
	// CORRECT $1.50. Output at $25 is a consistent 5× of the wrong $5 (so output
	// alone won't flag), but cache-read $1.50 is 30% of $5 — far above the ~10%
	// family ratio — so the guard catches it.
	s := guardSnap(map[string]Rate{
		"anthropic/claude-opus-4-8": {InputPerMillion: 5, OutputPerMillion: 25, CachedInputPerMillion: 1.5},
	})
	anomalies := Check(s)
	if len(anomalies) == 0 {
		t.Fatal("guard failed to catch the Opus ⅓ error")
	}
	if anomalies[0].Field != "cache_read" {
		t.Errorf("expected cache_read anomaly, got %+v", anomalies[0])
	}
	if !strings.Contains(anomalies[0].Message, "claude-opus-4-8") {
		t.Errorf("message should name the model: %q", anomalies[0].Message)
	}
}

func TestCheck_CatchesOutputRatioError(t *testing.T) {
	s := guardSnap(map[string]Rate{
		// output only 2× input — well outside the 5× Anthropic family ratio.
		"anthropic/claude-weird": {InputPerMillion: 10, OutputPerMillion: 20, CachedInputPerMillion: 1},
	})
	anomalies := Check(s)
	var foundOutput bool
	for _, a := range anomalies {
		if a.Field == "output" {
			foundOutput = true
		}
	}
	if !foundOutput {
		t.Errorf("expected an output anomaly, got %+v", anomalies)
	}
}

func TestCheck_RatioIsAnthropicScoped(t *testing.T) {
	// The 5×/10% ratios are an Anthropic-family invariant. Non-Anthropic rows
	// that would trip those ratios (Gemini Flash output ~8× input; an OpenAI
	// cache at 50% of input) must NOT be flagged by the ratio check — applying
	// Anthropic ratios to other providers is a false positive.
	s := guardSnap(map[string]Rate{
		"gemini/gemini-2.5-flash": {InputPerMillion: 0.30, OutputPerMillion: 2.50, CachedInputPerMillion: 0.075},
		"openai/gpt-4o":           {InputPerMillion: 2.50, OutputPerMillion: 10, CachedInputPerMillion: 1.25},
		"mistral/mistral-large":   {InputPerMillion: 2, OutputPerMillion: 6},
	})
	if a := Check(s); len(a) != 0 {
		t.Errorf("non-anthropic rows flagged by anthropic ratio check: %v", a)
	}
}

func TestCheck_GenericSanityFlagsCacheAboveInput(t *testing.T) {
	// The generic sanity check applies to every provider: a cache read priced
	// above fresh input is impossible regardless of vendor curve.
	s := guardSnap(map[string]Rate{
		"openai/broken": {InputPerMillion: 1, OutputPerMillion: 4, CachedInputPerMillion: 5},
	})
	anomalies := Check(s)
	if len(anomalies) != 1 || anomalies[0].Field != "cache_read" {
		t.Fatalf("expected one cache_read sanity anomaly, got %+v", anomalies)
	}
	if !strings.Contains(anomalies[0].Message, "exceeds input") {
		t.Errorf("message should explain cache > input: %q", anomalies[0].Message)
	}
}

func TestCheck_SkipsZeroRates(t *testing.T) {
	s := guardSnap(map[string]Rate{
		"anthropic/no-input":    {InputPerMillion: 0, OutputPerMillion: 50, CachedInputPerMillion: 5},
		"anthropic/no-cache":    {InputPerMillion: 3, OutputPerMillion: 15, CachedInputPerMillion: 0}, // cache omitted, fine
		"anthropic/output-only": {InputPerMillion: 2, OutputPerMillion: 0, CachedInputPerMillion: 0},  // output omitted, fine
	})
	if a := Check(s); len(a) != 0 {
		t.Errorf("zero/omitted rates should be skipped, got: %v", a)
	}
}

func TestCheck_Deterministic(t *testing.T) {
	s := guardSnap(map[string]Rate{
		"anthropic/b-model": {InputPerMillion: 10, OutputPerMillion: 20},
		"anthropic/a-model": {InputPerMillion: 10, OutputPerMillion: 20},
	})
	first := Check(s)
	for i := 0; i < 5; i++ {
		got := Check(s)
		if len(got) != len(first) || got[0].Model != first[0].Model {
			t.Fatal("Check output not deterministic")
		}
	}
	if first[0].Model != "anthropic/a-model" {
		t.Errorf("anomalies not sorted by model: %+v", first)
	}
}
