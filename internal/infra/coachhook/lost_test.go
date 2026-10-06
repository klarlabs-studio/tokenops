package coachhook

import (
	"strings"
	"testing"
	"time"
)

const lostSentence = "Claude's plan reading is 20h old: the claude.ai session expired. Sign in to claude.ai in your browser, or run `tokenops vendor-usage setup claude-subscription`."

// On a flat-rate plan the dollar ladder is a counterfactual nobody pays,
// and with no live window it was all the coach had left to say: "$400.99
// API-equivalent, 800% of the default $50 ceiling, not a charge; /compact"
// at 19% context (2026-10-06). It no longer speaks dollars there; it says
// once that the plan reading is lost, and how to get it back.
func TestFlatPlanWithoutReadingSaysWhyNotDollars(t *testing.T) {
	dir := t.TempDir()
	cfg := DefaultConfig()
	cfg.FlatPlan = true
	cfg.ReadingLost = lostSentence
	// $400 of $50 would fire the dollar ladder many times over.
	tp := writeTranscript(t, dir, turnLine(ts(1), 800_000_000, opus))
	d := Evaluate(dir, "s1", tp, cfg, fixedNow)
	if !d.Nudge || !d.ReadingLost || d.FiredFraction != 0 {
		t.Fatalf("want the reading-lost note and no dollar tier, got %+v", d)
	}
	if !strings.Contains(d.Message, "vendor-usage setup claude-subscription") || strings.Contains(d.Message, "API-equivalent") {
		t.Errorf("message %q", d.Message)
	}
}

// The reading is lost for every session at once, so it is said once
// across them, and again only hours later if it is still lost.
func TestReadingLostIsSaidOnceAcrossSessions(t *testing.T) {
	dir := t.TempDir()
	cfg := DefaultConfig()
	cfg.FlatPlan = true
	cfg.ReadingLost = lostSentence
	tp := writeTranscript(t, dir, turnLine(ts(1), 1_000, opus))
	if d := Evaluate(dir, "s1", tp, cfg, fixedNow); !d.Nudge {
		t.Fatal("first session was not told")
	}
	if d := Evaluate(dir, "s2", tp, cfg, fixedNow.Add(time.Hour)); d.Nudge {
		t.Errorf("second session was told again within the hour: %q", d.Message)
	}
	if d := Evaluate(dir, "s3", tp, cfg, fixedNow.Add(readingLostEvery+time.Minute)); !d.Nudge {
		t.Error("still lost hours later, and not said again")
	}
}

// A flat-rate plan with nothing known about its reading — never set up —
// stays silent rather than falling back to dollars.
func TestFlatPlanWithNothingToSayIsSilent(t *testing.T) {
	dir := t.TempDir()
	cfg := DefaultConfig()
	cfg.FlatPlan = true
	tp := writeTranscript(t, dir, turnLine(ts(1), 800_000_000, opus))
	if d := Evaluate(dir, "s1", tp, cfg, fixedNow); d.Nudge || d.FiredFraction != 0 {
		t.Errorf("flat plan spoke: %+v", d)
	}
}
