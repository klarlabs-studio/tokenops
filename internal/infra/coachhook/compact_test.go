package coachhook

import (
	"strings"
	"testing"
	"time"
)

// turnsAt appends n turns of ctx context starting at second start. 700k of
// cache-read on opus is $0.35 a turn, so 40 turns stay under the default
// budget's first tier and no dollar tip competes with the compact tip.
func turnsAt(lines []string, start, n int, ctx int64) []string {
	for i := range n {
		lines = append(lines, turnLine(tsAt(start+i), ctx, opus))
	}
	return lines
}

func tsAt(n int) string {
	return time.Date(2026, 7, 7, 12, n/60, n%60, 0, time.UTC).Format(time.RFC3339)
}

func compactConfig() Config {
	cfg := tipConfig()
	cfg.CompactAtTokens = 600_000
	return cfg
}

func TestCompactTipAfterALongStretch(t *testing.T) {
	dir := t.TempDir()
	cfg := compactConfig()
	lines := turnsAt(nil, 0, CompactAfterTurns-1, 700_000)
	tp := writeTranscript(t, dir, lines...)
	if d := Evaluate(dir, "s", tp, cfg, fixedNow); d.Nudge {
		t.Fatalf("tipped after %d turns: %q", CompactAfterTurns-1, d.Message)
	}
	lines = turnsAt(lines, CompactAfterTurns-1, 1, 700_000)
	rewrite(t, tp, lines...)
	d := Evaluate(dir, "s", tp, cfg, fixedNow)
	if !d.Nudge || !d.CompactTip || d.TipKind != "compact_now" || d.OfferID == "" {
		t.Fatalf("no compact tip at %d turns: %+v", CompactAfterTurns, d)
	}
	if !strings.Contains(d.Message, "/compact") || !strings.Contains(d.Message, "600k") {
		t.Errorf("message = %q", d.Message)
	}
	// Once per window.
	lines = turnsAt(lines, CompactAfterTurns, 5, 700_000)
	rewrite(t, tp, lines...)
	if d := Evaluate(dir, "s", tp, cfg, fixedNow); d.Nudge {
		t.Fatalf("tipped twice in one window: %q", d.Message)
	}
	// A compaction settles the tip as followed and opens a new window.
	lines = turnsAt(lines, CompactAfterTurns+5, 1, 80_000)
	rewrite(t, tp, lines...)
	d = Evaluate(dir, "s", tp, cfg, fixedNow)
	if len(d.Resolved) != 1 || !d.Resolved[0].Followed {
		t.Fatalf("compaction after the tip: Resolved = %+v; want followed", d.Resolved)
	}
	lines = turnsAt(lines, CompactAfterTurns+6, CompactAfterTurns, 700_000)
	rewrite(t, tp, lines...)
	if d := Evaluate(dir, "s", tp, cfg, fixedNow); !d.CompactTip {
		t.Fatal("no tip in the next window")
	}
}

func TestCompactTipOffWhenQuietOrUnset(t *testing.T) {
	for name, mut := range map[string]func(*Config){
		"quiet": func(c *Config) { c.Verbosity = verbosityQuiet },
		"unset": func(c *Config) { c.CompactAtTokens = 0 },
	} {
		dir := t.TempDir()
		cfg := compactConfig()
		mut(&cfg)
		tp := writeTranscript(t, dir, turnsAt(nil, 0, CompactAfterTurns+5, 700_000)...)
		if d := Evaluate(dir, "s", tp, cfg, fixedNow); d.CompactTip {
			t.Errorf("%s: compact tip given", name)
		}
	}
}

// A dip below the line without compacting keeps the stretch: only a
// compaction opens a new window.
func TestCompactStretchSurvivesSmallDips(t *testing.T) {
	dir := t.TempDir()
	cfg := compactConfig()
	lines := turnsAt(nil, 0, 10, 700_000)
	lines = turnsAt(lines, 10, 3, 590_000)
	lines = turnsAt(lines, 13, 10, 700_000)
	tp := writeTranscript(t, dir, lines...)
	if d := Evaluate(dir, "s", tp, cfg, fixedNow); !d.CompactTip {
		t.Fatal("20 turns above the line around a small dip did not tip")
	}
}

func boundary(ts, trigger string) string {
	return `{"type":"system","subtype":"compact_boundary","timestamp":"` + ts + `","compactMetadata":{"trigger":"` + trigger + `"}}`
}

// The client compacting on its own at the ceiling is not the operator
// acting on the tip.
func TestAnAutomaticCompactionDoesNotFollowTheTip(t *testing.T) {
	for trigger, followed := range map[string]bool{"auto": false, "manual": true} {
		dir := t.TempDir()
		cfg := compactConfig()
		lines := turnsAt(nil, 0, CompactAfterTurns, 700_000)
		tp := writeTranscript(t, dir, lines...)
		if d := Evaluate(dir, "s", tp, cfg, fixedNow); !d.CompactTip {
			t.Fatal("no tip")
		}
		lines = append(lines, boundary(tsAt(CompactAfterTurns), trigger))
		lines = turnsAt(lines, CompactAfterTurns+1, 1, 30_000)
		rewrite(t, tp, lines...)
		d := Evaluate(dir, "s", tp, cfg, fixedNow)
		if len(d.Resolved) != 1 || d.Resolved[0].Followed != followed {
			t.Errorf("%s compaction: Resolved = %+v; want followed=%v", trigger, d.Resolved, followed)
		}
	}
}
