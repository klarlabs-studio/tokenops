package coachhook

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeTranscript writes a small multi-line transcript jsonl into dir and
// returns its path.
func writeTranscript(t *testing.T, dir string, lines ...string) string {
	t.Helper()
	p := filepath.Join(dir, "transcript.jsonl")
	if err := os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatalf("write transcript: %v", err)
	}
	return p
}

func usageLine(cacheRead int64, model string) string {
	return `{"type":"assistant","message":{"model":"` + model +
		`","usage":{"input_tokens":10,"output_tokens":20,"cache_creation_input_tokens":5,"cache_read_input_tokens":` +
		itoa(cacheRead) + `}}}`
}

var fixedNow = time.Date(2026, 7, 7, 12, 0, 0, 0, time.UTC)

func TestEvaluate_TailParsePicksLatestUsage(t *testing.T) {
	dir := t.TempDir()
	tp := writeTranscript(t, dir,
		`{"type":"user","message":{"role":"user","content":"hi"}}`,
		usageLine(500_000, "claude-opus-4-8"),
		`{"type":"user","message":{"role":"user","content":"more"}}`,
		usageLine(1_400_000, "claude-opus-4-8"), // latest usage wins
	)
	dec := Evaluate(dir, "sess-tail", tp, DefaultConfig(), fixedNow)
	if dec.CacheReadTokens != 1_400_000 {
		t.Fatalf("want latest cache-read 1_400_000, got %d", dec.CacheReadTokens)
	}
	if !dec.Nudge {
		t.Fatalf("expected nudge above threshold")
	}
	if !strings.Contains(dec.Message, "1.4M") {
		t.Fatalf("message should humanize tokens, got %q", dec.Message)
	}
	if !strings.Contains(dec.Message, "$") {
		t.Fatalf("opus model should carry a $ figure, got %q", dec.Message)
	}
	// 1.4M * 0.50/1M = 0.70 with the embedded opus cache-read rate.
	if dec.EstCostUSDPerTurn <= 0 {
		t.Fatalf("expected positive cost for opus, got %v", dec.EstCostUSDPerTurn)
	}
}

func TestEvaluate_BelowThresholdNoNudge(t *testing.T) {
	dir := t.TempDir()
	tp := writeTranscript(t, dir, usageLine(500_000, "claude-opus-4-8"))
	dec := Evaluate(dir, "sess-below", tp, DefaultConfig(), fixedNow)
	if dec.Nudge {
		t.Fatalf("did not expect nudge below threshold")
	}
	if dec.CacheReadTokens != 500_000 {
		t.Fatalf("want 500_000, got %d", dec.CacheReadTokens)
	}
}

func TestEvaluate_CooldownSuppressesThenRefires(t *testing.T) {
	dir := t.TempDir()
	tp := writeTranscript(t, dir, usageLine(2_000_000, "claude-opus-4-8"))
	cfg := Config{CacheReadThreshold: 1_000_000, CooldownTurns: 20, Enabled: true}

	// Turn 1: never nudged -> nudge.
	if d := Evaluate(dir, "sess-cd", tp, cfg, fixedNow); !d.Nudge {
		t.Fatalf("turn 1 should nudge")
	}
	// Turn 2: within cooldown -> suppressed even though still over threshold.
	if d := Evaluate(dir, "sess-cd", tp, cfg, fixedNow); d.Nudge {
		t.Fatalf("turn 2 within cooldown should be suppressed")
	}
	// Turns 3..20: still suppressed.
	for i := 3; i <= 20; i++ {
		if d := Evaluate(dir, "sess-cd", tp, cfg, fixedNow); d.Nudge {
			t.Fatalf("turn %d within cooldown should be suppressed", i)
		}
	}
	// Turn 21: cooldown elapsed (21-1 = 20 >= 20) -> re-fires.
	if d := Evaluate(dir, "sess-cd", tp, cfg, fixedNow); !d.Nudge {
		t.Fatalf("turn 21 should re-fire after cooldown")
	}
}

func TestEvaluate_DisabledNeverNudges(t *testing.T) {
	dir := t.TempDir()
	tp := writeTranscript(t, dir, usageLine(5_000_000, "claude-opus-4-8"))
	cfg := DefaultConfig()
	cfg.Enabled = false
	if d := Evaluate(dir, "sess-off", tp, cfg, fixedNow); d.Nudge {
		t.Fatalf("disabled coach must not nudge")
	}
	// But it still records the observation.
	s, err := ReadStats(dir)
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	if s.Events != 1 {
		t.Fatalf("expected 1 ledger event, got %d", s.Events)
	}
}

func TestEvaluate_NonOpusNoCostFigure(t *testing.T) {
	dir := t.TempDir()
	tp := writeTranscript(t, dir, usageLine(1_500_000, "claude-sonnet-4-6"))
	dec := Evaluate(dir, "sess-sonnet", tp, DefaultConfig(), fixedNow)
	if !dec.Nudge {
		t.Fatalf("expected nudge above threshold")
	}
	if dec.EstCostUSDPerTurn != 0 {
		t.Fatalf("non-opus should not carry a cost, got %v", dec.EstCostUSDPerTurn)
	}
	if strings.Contains(dec.Message, "$") {
		t.Fatalf("non-opus message must not show a $ figure, got %q", dec.Message)
	}
}

func TestEvaluate_MalformedAndMissingUsageNoPanic(t *testing.T) {
	dir := t.TempDir()
	tp := writeTranscript(t, dir,
		`not json at all`,
		`{"type":"user","message":{"role":"user"}}`, // no usage
		`{"broken":`, // malformed json
	)
	dec := Evaluate(dir, "sess-malformed", tp, DefaultConfig(), fixedNow)
	if dec.Nudge {
		t.Fatalf("no usage means no nudge")
	}
	if dec.CacheReadTokens != 0 {
		t.Fatalf("want 0 cache-read, got %d", dec.CacheReadTokens)
	}
}

func TestEvaluate_UnreadableTranscriptFailsOpen(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "does-not-exist.jsonl")
	dec := Evaluate(dir, "sess-missing", missing, DefaultConfig(), fixedNow)
	if dec.Nudge {
		t.Fatalf("unreadable transcript must fail open (no nudge)")
	}
	// Ledger event still written (turn observed with zero cache-read).
	s, _ := ReadStats(dir)
	if s.Events != 1 {
		t.Fatalf("expected 1 event, got %d", s.Events)
	}
}

func TestEvaluate_TailIgnoresLeadingPartialLine(t *testing.T) {
	dir := t.TempDir()
	// Build a transcript whose early lines exceed the tail window so the seek
	// lands mid-line; the coach must still parse the final usage record.
	pad := `{"type":"filler","message":{"content":"` + strings.Repeat("x", 300_000) + `"}}`
	tp := writeTranscript(t, dir,
		pad,
		usageLine(3_000_000, "claude-opus-4-8"),
	)
	dec := Evaluate(dir, "sess-bigtail", tp, DefaultConfig(), fixedNow)
	if dec.CacheReadTokens != 3_000_000 {
		t.Fatalf("want 3_000_000 from tail, got %d", dec.CacheReadTokens)
	}
	if !dec.Nudge {
		t.Fatalf("expected nudge")
	}
}

func TestReadStats_Aggregates(t *testing.T) {
	dir := t.TempDir()
	cfg := Config{CacheReadThreshold: 1_000_000, CooldownTurns: 1, Enabled: true}
	// Two sessions, a few turns each.
	tp1 := writeTranscript(t, t.TempDir(), usageLine(2_000_000, "claude-opus-4-8"))
	Evaluate(dir, "s1", tp1, cfg, fixedNow)
	Evaluate(dir, "s1", tp1, cfg, fixedNow)
	tp2 := writeTranscript(t, t.TempDir(), usageLine(500_000, "claude-opus-4-8"))
	Evaluate(dir, "s2", tp2, cfg, fixedNow)

	s, err := ReadStats(dir)
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	if s.Events != 3 {
		t.Fatalf("want 3 events, got %d", s.Events)
	}
	if s.DistinctSessions != 2 {
		t.Fatalf("want 2 sessions, got %d", s.DistinctSessions)
	}
	if s.MaxCacheReadPerTurn != 2_000_000 {
		t.Fatalf("want max 2_000_000, got %d", s.MaxCacheReadPerTurn)
	}
	if s.Nudges < 1 {
		t.Fatalf("want at least 1 nudge, got %d", s.Nudges)
	}
	if s.EstReclaimableUSD <= 0 {
		t.Fatalf("want positive reclaimable USD, got %v", s.EstReclaimableUSD)
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
