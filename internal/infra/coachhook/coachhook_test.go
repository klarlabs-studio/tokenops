package coachhook

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A cache-read-only turn priced at opus's cached rate ($0.50/M) makes the math
// legible: cost == cacheRead * 0.50 / 1e6. So 20M cache-read tokens == $10.
const (
	opus  = "claude-opus-4-8"
	haiku = "claude-haiku-4-5"
)

// turnLine builds one assistant transcript record with a top-level timestamp
// and a cache-read-only usage block (input/output/cache-write zero) so the
// turn's whole cost is cacheRead priced at the model's cached rate.
func turnLine(ts string, cacheRead int64, model string) string {
	return `{"type":"assistant","timestamp":"` + ts + `","message":{"model":"` + model +
		`","usage":{"input_tokens":0,"output_tokens":0,"cache_creation_input_tokens":0,"cache_read_input_tokens":` +
		itoa(cacheRead) + `}}}`
}

// writeTranscript writes lines to dir/transcript.jsonl and returns the path.
func writeTranscript(t *testing.T, dir string, lines ...string) string {
	t.Helper()
	p := filepath.Join(dir, "transcript.jsonl")
	if err := os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatalf("write transcript: %v", err)
	}
	return p
}

// rewrite replaces the transcript file wholesale, simulating Claude Code
// appending turns between Stop events (the coach always re-reads the tail).
func rewrite(t *testing.T, path string, lines ...string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatalf("rewrite transcript: %v", err)
	}
}

var fixedNow = time.Date(2026, 7, 7, 12, 0, 0, 0, time.UTC)

func ts(n int) string {
	return time.Date(2026, 7, 7, 12, 0, n, 0, time.UTC).Format(time.RFC3339)
}

// TestEvaluate_CumulativeAccumulationDedups drives several Stops that each add a
// turn, asserting cumulative spend grows and repeated Stops over the same tail
// never double-count (the LastCountedTS marker).
func TestEvaluate_CumulativeAccumulationDedups(t *testing.T) {
	dir := t.TempDir()
	cfg := DefaultConfig() // $50 budget

	// Stop 1: one 20M cache-read turn -> $10.
	tp := writeTranscript(t, dir, turnLine(ts(1), 20_000_000, opus))
	d := Evaluate(dir, "s", tp, cfg, fixedNow)
	if !approx(d.CumulativeUSD, 10) {
		t.Fatalf("after turn 1 want $10, got %.4f", d.CumulativeUSD)
	}
	if d.Nudge {
		t.Fatalf("$10 of $50 (20%%) must not nudge")
	}

	// Stop 2: same transcript, no new turn -> cumulative unchanged (dedup).
	d = Evaluate(dir, "s", tp, cfg, fixedNow)
	if !approx(d.CumulativeUSD, 10) {
		t.Fatalf("dedup failed: want $10 unchanged, got %.4f", d.CumulativeUSD)
	}

	// Stop 3: append a 40M turn -> +$20 = $30 (60%%), crosses the 50%% tier.
	rewrite(t, tp, turnLine(ts(1), 20_000_000, opus), turnLine(ts(2), 40_000_000, opus))
	d = Evaluate(dir, "s", tp, cfg, fixedNow)
	if !approx(d.CumulativeUSD, 30) {
		t.Fatalf("after turn 2 want $30, got %.4f", d.CumulativeUSD)
	}
	if !d.Nudge || !approx(d.FiredFraction, 0.50) {
		t.Fatalf("expected 50%% tier fired, got nudge=%v frac=%.3f", d.Nudge, d.FiredFraction)
	}
}

// TestEvaluate_TierLatching: 50% fires once (not again while below 75%), then
// 75%, then 100% — each tier once.
func TestEvaluate_TierLatching(t *testing.T) {
	dir := t.TempDir()
	cfg := DefaultConfig()

	// Turn 1: 52M -> $26 (52%) -> fire 50%.
	tp := writeTranscript(t, dir, turnLine(ts(1), 52_000_000, opus))
	if d := Evaluate(dir, "s", tp, cfg, fixedNow); !d.Nudge || !approx(d.FiredFraction, 0.50) {
		t.Fatalf("turn 1 should fire 50%%, got nudge=%v frac=%.3f", d.Nudge, d.FiredFraction)
	}
	// Turn 2 (no new turn): still 52%, below 75% -> no re-fire.
	if d := Evaluate(dir, "s", tp, cfg, fixedNow); d.Nudge {
		t.Fatalf("still at 52%% must not re-fire the 50%% tier")
	}
	// Append -> $39 (78%) -> fire 75%.
	rewrite(t, tp, turnLine(ts(1), 52_000_000, opus), turnLine(ts(2), 26_000_000, opus))
	if d := Evaluate(dir, "s", tp, cfg, fixedNow); !d.Nudge || !approx(d.FiredFraction, 0.75) {
		t.Fatalf("expected 75%% tier, got nudge=%v frac=%.3f", d.Nudge, d.FiredFraction)
	}
	// Append -> $52 (104%) -> fire 100%.
	rewrite(t, tp,
		turnLine(ts(1), 52_000_000, opus),
		turnLine(ts(2), 26_000_000, opus),
		turnLine(ts(3), 26_000_000, opus))
	d := Evaluate(dir, "s", tp, cfg, fixedNow)
	if !d.Nudge || !approx(d.FiredFraction, 1.00) {
		t.Fatalf("expected 100%% tier, got nudge=%v frac=%.3f", d.Nudge, d.FiredFraction)
	}
	if !strings.Contains(d.Message, "over your $50 session budget") {
		t.Fatalf("100%% message unexpected: %q", d.Message)
	}
}

// TestEvaluate_BurstFiresHighestTierOnly: one Stop jumping 40%->120% fires only
// the 100% tier, never a burst of 50/75/100.
func TestEvaluate_BurstFiresHighestTierOnly(t *testing.T) {
	dir := t.TempDir()
	cfg := DefaultConfig()

	// Turn 1: 40M -> $20 (40%) -> no tier.
	tp := writeTranscript(t, dir, turnLine(ts(1), 40_000_000, opus))
	if d := Evaluate(dir, "s", tp, cfg, fixedNow); d.Nudge {
		t.Fatalf("40%% must not nudge")
	}
	// Append 80M -> +$40 = $60 (120%) in one Stop -> only 100% fires.
	rewrite(t, tp, turnLine(ts(1), 40_000_000, opus), turnLine(ts(2), 80_000_000, opus))
	d := Evaluate(dir, "s", tp, cfg, fixedNow)
	if !d.Nudge || !approx(d.FiredFraction, 1.00) {
		t.Fatalf("burst 40->120%% should fire only 100%%, got nudge=%v frac=%.3f", d.Nudge, d.FiredFraction)
	}
	// The very next Stop (no new turn) must not fire 50% or 75% belatedly.
	if d := Evaluate(dir, "s", tp, cfg, fixedNow); d.Nudge {
		t.Fatalf("skipped tiers must not fire later, got frac=%.3f", d.FiredFraction)
	}
}

// TestEvaluate_OverBudgetEscalation: reaching 200% fires the over tier.
func TestEvaluate_OverBudgetEscalation(t *testing.T) {
	dir := t.TempDir()
	cfg := DefaultConfig() // OverBudgetStep 1.0

	// Turn 1: 100M -> $50 (100%) -> fire 100%.
	tp := writeTranscript(t, dir, turnLine(ts(1), 100_000_000, opus))
	if d := Evaluate(dir, "s", tp, cfg, fixedNow); !d.Nudge || !approx(d.FiredFraction, 1.00) {
		t.Fatalf("expected 100%%, got nudge=%v frac=%.3f", d.Nudge, d.FiredFraction)
	}
	// Append 100M -> $100 (200%) -> fire the 200% over tier.
	rewrite(t, tp, turnLine(ts(1), 100_000_000, opus), turnLine(ts(2), 100_000_000, opus))
	d := Evaluate(dir, "s", tp, cfg, fixedNow)
	if !d.Nudge || !approx(d.FiredFraction, 2.00) {
		t.Fatalf("expected 200%% over tier, got nudge=%v frac=%.3f", d.Nudge, d.FiredFraction)
	}
	if !strings.Contains(d.Message, "200%") || !strings.Contains(d.Message, "$100+") {
		t.Fatalf("200%% message should carry 200%% and $100+, got %q", d.Message)
	}
}

// TestEvaluate_DisabledObservesOnly: disabled never nudges or latches, but still
// accumulates and records the ledger.
func TestEvaluate_DisabledObservesOnly(t *testing.T) {
	dir := t.TempDir()
	cfg := DefaultConfig()
	cfg.Enabled = false
	tp := writeTranscript(t, dir, turnLine(ts(1), 200_000_000, opus)) // $100, way over
	d := Evaluate(dir, "s", tp, cfg, fixedNow)
	if d.Nudge {
		t.Fatalf("disabled coach must not nudge")
	}
	if !approx(d.CumulativeUSD, 100) {
		t.Fatalf("disabled coach still accumulates, want $100 got %.2f", d.CumulativeUSD)
	}
	s, err := ReadStats(dir)
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	if s.Events != 1 {
		t.Fatalf("want 1 ledger event, got %d", s.Events)
	}
	if s.Alerts != 0 {
		t.Fatalf("disabled must record no alerts, got %d", s.Alerts)
	}
}

// TestEvaluate_ModelAgnostic: a cheaper model accrues slower per token but still
// trips tiers proportionally.
func TestEvaluate_ModelAgnostic(t *testing.T) {
	dir := t.TempDir()
	cfg := DefaultConfig()
	cfg.BudgetUSD = 5.0 // small budget so haiku (0.10/M cached) can reach it

	// haiku: 60M cache-read -> $6.00 (120% of $5) -> fires 100%.
	tp := writeTranscript(t, dir, turnLine(ts(1), 60_000_000, haiku))
	d := Evaluate(dir, "haiku", tp, cfg, fixedNow)
	if !approx(d.CumulativeUSD, 6.0) {
		t.Fatalf("haiku 60M want $6.00, got %.4f", d.CumulativeUSD)
	}
	if !d.Nudge || !approx(d.FiredFraction, 1.00) {
		t.Fatalf("cheaper model should still trip a tier, got nudge=%v frac=%.3f", d.Nudge, d.FiredFraction)
	}

	// Same tokens on opus (0.50/M) cost 5x -> reaches the tier far sooner.
	tp2 := writeTranscript(t, t.TempDir(), turnLine(ts(1), 60_000_000, opus))
	if o := Evaluate(dir, "opus", tp2, cfg, fixedNow); o.CumulativeUSD <= d.CumulativeUSD {
		t.Fatalf("opus should accrue more than haiku for equal tokens: opus=%.2f haiku=%.2f", o.CumulativeUSD, d.CumulativeUSD)
	}
}

// TestEvaluate_UnpriceableModelAdvancesMarkerWithoutCost: an unpriceable model
// adds zero cost but still advances the dedup marker, so it is never re-summed
// and a later priced turn still counts.
func TestEvaluate_UnpriceableModelAdvancesMarkerWithoutCost(t *testing.T) {
	dir := t.TempDir()
	cfg := DefaultConfig()

	tp := writeTranscript(t, dir, turnLine(ts(1), 500_000_000, "totally-unknown-model"))
	d := Evaluate(dir, "s", tp, cfg, fixedNow)
	if !approx(d.CumulativeUSD, 0) {
		t.Fatalf("unpriceable model must add $0, got %.4f", d.CumulativeUSD)
	}
	if d.Nudge {
		t.Fatalf("no cost means no nudge")
	}
	// Append a priced opus turn; the unpriced turn must not be re-summed.
	rewrite(t, tp,
		turnLine(ts(1), 500_000_000, "totally-unknown-model"),
		turnLine(ts(2), 20_000_000, opus))
	d = Evaluate(dir, "s", tp, cfg, fixedNow)
	if !approx(d.CumulativeUSD, 10) {
		t.Fatalf("want only the $10 opus turn counted, got %.4f", d.CumulativeUSD)
	}
}

// TestEvaluate_FailOpen: missing/malformed transcripts never nudge or panic.
func TestEvaluate_FailOpen(t *testing.T) {
	dir := t.TempDir()

	// Missing transcript.
	if d := Evaluate(dir, "miss", filepath.Join(dir, "nope.jsonl"), DefaultConfig(), fixedNow); d.Nudge {
		t.Fatalf("missing transcript must fail open")
	}
	// Malformed / no-usage lines.
	tp := writeTranscript(t, dir,
		`not json`,
		`{"type":"user","message":{"role":"user"}}`,
		`{"broken":`)
	if d := Evaluate(dir, "bad", tp, DefaultConfig(), fixedNow); d.Nudge || !approx(d.CumulativeUSD, 0) {
		t.Fatalf("malformed input must fail open with $0, got nudge=%v cum=%.2f", d.Nudge, d.CumulativeUSD)
	}
	// Ledger events still written (fail-open observes).
	s, _ := ReadStats(dir)
	if s.Events != 2 {
		t.Fatalf("want 2 ledger events, got %d", s.Events)
	}
}

// TestEvaluate_TailIgnoresLeadingPartialLine: a transcript whose early lines
// exceed the tail window must still parse the final usage record.
func TestEvaluate_TailIgnoresLeadingPartialLine(t *testing.T) {
	dir := t.TempDir()
	pad := `{"type":"filler","message":{"content":"` + strings.Repeat("x", 300_000) + `"}}`
	tp := writeTranscript(t, dir, pad, turnLine(ts(1), 120_000_000, opus)) // $60 -> 120%
	d := Evaluate(dir, "s", tp, DefaultConfig(), fixedNow)
	if !approx(d.CumulativeUSD, 60) {
		t.Fatalf("want $60 from tail, got %.4f", d.CumulativeUSD)
	}
	if !d.Nudge || !approx(d.FiredFraction, 1.00) {
		t.Fatalf("expected 100%% tier from tail, got nudge=%v frac=%.3f", d.Nudge, d.FiredFraction)
	}
}

// TestReadStats_Aggregates checks the summary shape: sessions, alerts by tier,
// max cumulative, and total est spend across sessions.
func TestReadStats_Aggregates(t *testing.T) {
	dir := t.TempDir()
	cfg := DefaultConfig()

	// Session A: climb to 100% over three Stops (fires 50, 75, 100).
	pa := writeTranscript(t, t.TempDir(), turnLine(ts(1), 52_000_000, opus))
	Evaluate(dir, "A", pa, cfg, fixedNow) // 50%
	rewrite(t, pa, turnLine(ts(1), 52_000_000, opus), turnLine(ts(2), 26_000_000, opus))
	Evaluate(dir, "A", pa, cfg, fixedNow) // 75%
	rewrite(t, pa, turnLine(ts(1), 52_000_000, opus), turnLine(ts(2), 26_000_000, opus), turnLine(ts(3), 26_000_000, opus))
	Evaluate(dir, "A", pa, cfg, fixedNow) // 100%, cumulative $52

	// Session B: a single small turn, no alert.
	pb := writeTranscript(t, t.TempDir(), turnLine(ts(1), 10_000_000, opus)) // $5
	Evaluate(dir, "B", pb, cfg, fixedNow)

	s, err := ReadStats(dir)
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	if s.DistinctSessions != 2 {
		t.Fatalf("want 2 sessions, got %d", s.DistinctSessions)
	}
	if s.Alerts != 3 {
		t.Fatalf("want 3 alerts, got %d", s.Alerts)
	}
	for _, tier := range []string{"50%", "75%", "100%"} {
		if s.AlertsByTier[tier] != 1 {
			t.Fatalf("want one %s alert, got %d", tier, s.AlertsByTier[tier])
		}
	}
	if !approx(s.MaxCumulativeUSD, 52) {
		t.Fatalf("want max cumulative $52, got %.2f", s.MaxCumulativeUSD)
	}
	if !approx(s.TotalEstSpendUSD, 57) { // 52 (A) + 5 (B)
		t.Fatalf("want total est spend $57, got %.2f", s.TotalEstSpendUSD)
	}
}

func TestReadStats_Empty(t *testing.T) {
	s, err := ReadStats(t.TempDir())
	if err != nil {
		t.Fatalf("empty stats should not error: %v", err)
	}
	if s.Events != 0 {
		t.Fatalf("want 0 events, got %d", s.Events)
	}
}

// approx compares dollar figures with a cent of tolerance.
func approx(a, b float64) bool {
	d := a - b
	if d < 0 {
		d = -d
	}
	return d < 0.005
}
