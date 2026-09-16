package coachhook

import (
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/spend/spend"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

func i64(v int64) *int64 { return &v }

// cursorRates prices one model under exactly one provider, which is what
// LookupAnyProvider requires to answer at all.
func cursorRates(model string) func(time.Time) spend.Table {
	return func(time.Time) spend.Table {
		return spend.Table{Currency: "USD", Rates: map[spend.Key]spend.Rate{
			{Provider: eventschema.ProviderXAI, Model: model}: {
				InputPerMillion: 10, OutputPerMillion: 50, CachedInputPerMillion: 1,
			},
		}}
	}
}

// The convention that matters. Cursor's own team: "input_tokens is
// inclusive of cache_read_tokens and cache_write_tokens… only 14 of the
// 1.18M input tokens were genuinely uncached." Pricing it the Claude Code
// way — where the figures are disjoint — bills 1.18M at the full input
// rate instead of 14.
func TestCursorCountsCacheInsideInputExactlyOnce(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Rates = cursorRates("grok-4.6")
	turn := cursorTurn{
		Model: "cursor-grok-4.6-high-fast", ModelID: "grok-4.6",
		InputTokens:      i64(1_000_000),
		CacheReadTokens:  i64(900_000),
		CacheWriteTokens: i64(99_986),
		OutputTokens:     i64(0),
	}
	got, priced := cursorTurnCostUSD(cfg.ratesAt(time.Now()), turn)
	if !priced {
		t.Fatal("turn went unpriced")
	}
	// uncached 14 @ $10/M + 900k cached @ $1/M + 99,986 writes @ $10/M
	want := perMillion(14, 10) + perMillion(900_000, 1) + perMillion(99_986, 10)
	if got != want {
		t.Errorf("cost = %v, want %v", got, want)
	}
	// The failure mode being guarded: treating the figures as disjoint.
	disjoint := perMillion(1_000_000, 10) + perMillion(900_000, 1) + perMillion(99_986, 10)
	if got >= disjoint {
		t.Errorf("cost %v is not below the disjoint reading %v — cache was billed twice", got, disjoint)
	}
}

// A payload reporting more cache than input is not something to reason
// about, and must not credit the operator for tokens they used.
func TestCursorNeverGoesNegative(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Rates = cursorRates("grok-4.6")
	got, _ := cursorTurnCostUSD(cfg.ratesAt(time.Now()), cursorTurn{
		ModelID: "grok-4.6", InputTokens: i64(10), CacheReadTokens: i64(1_000_000),
	})
	if got < 0 {
		t.Errorf("cost = %v, want >= 0", got)
	}
}

// Cursor documents the token fields as optional. Absent means "not
// reported", never zero-cost — the distinction between a turn that was
// free and one nobody measured.
func TestCursorTreatsAbsentTokensAsUnreported(t *testing.T) {
	if (cursorTurn{ModelID: "grok-4.6"}).reported() {
		t.Error("a payload with no token fields reported as measured")
	}
	if !(cursorTurn{InputTokens: i64(0)}).reported() {
		t.Error("an explicit zero is a measurement and must count as reported")
	}
}

// Cursor reports cumulative totals per turn and sends identical numbers
// on stop and afterAgentResponse for the same generation — and a stop
// hook returning a follow-up fires again for the same conversation.
// Counting any of those twice inflates the session by a whole turn.
func TestCursorDeduplicatesOnGenerationID(t *testing.T) {
	dir := t.TempDir()
	cfg := DefaultConfig()
	cfg.Rates = cursorRates("grok-4.6")
	turn := cursorTurn{
		ConversationID: "conv-1", GenerationID: "gen-1", ModelID: "grok-4.6",
		InputTokens: i64(1_000_000), OutputTokens: i64(1000),
	}
	first := evaluateCursor(dir, turn, cfg, fixedNow)
	again := evaluateCursor(dir, turn, cfg, fixedNow.Add(time.Second))
	if again.CumulativeUSD != first.CumulativeUSD {
		t.Errorf("replaying one generation moved cumulative %v -> %v", first.CumulativeUSD, again.CumulativeUSD)
	}
	// A genuinely new generation still accrues.
	turn.GenerationID = "gen-2"
	next := evaluateCursor(dir, turn, cfg, fixedNow.Add(2*time.Second))
	if next.CumulativeUSD <= first.CumulativeUSD {
		t.Errorf("a new generation did not accrue: %v -> %v", first.CumulativeUSD, next.CumulativeUSD)
	}
}

// Cursor resells many vendors' models and names them without saying
// whose they are. A name two vendors both price is reported unpriced
// rather than attributed by coin flip.
func TestCursorRefusesToGuessAnAmbiguousVendor(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Rates = func(time.Time) spend.Table {
		return spend.Table{Rates: map[spend.Key]spend.Rate{
			{Provider: eventschema.ProviderXAI, Model: "shared"}:    {InputPerMillion: 10},
			{Provider: eventschema.ProviderOpenAI, Model: "shared"}: {InputPerMillion: 99},
		}}
	}
	_, priced := cursorTurnCostUSD(cfg.ratesAt(time.Now()), cursorTurn{
		ModelID: "shared", InputTokens: i64(1_000_000),
	})
	if priced {
		t.Error("an ambiguous model name was priced by guessing a vendor")
	}
}

// model_id is the structured name a rate card might carry;
// `model` is the composer slug with effort and speed flags baked in.
func TestCursorPrefersTheStructuredModelID(t *testing.T) {
	c := cursorTurn{Model: "cursor-grok-4.6-high-fast", ModelID: "grok-4.6"}
	if got := c.modelName(); got != "grok-4.6" {
		t.Errorf("modelName = %q, want grok-4.6", got)
	}
	if got := (cursorTurn{Model: "composer-1"}).modelName(); got != "composer-1" {
		t.Errorf("modelName fell back wrongly: %q", got)
	}
}
