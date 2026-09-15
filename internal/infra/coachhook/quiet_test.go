package coachhook

import (
	"testing"
	"time"
)

// quietCfg is the shipping config with a rate limit attached.
func quietCfg(q Quiet) Config {
	c := DefaultConfig()
	c.Quiet = q
	return c
}

// The floor defers; it does not mute. A finding held back must keep its
// latch, or a rate limit silently becomes a permanent silencer — which is
// exactly the kind of config that reads as policy while doing something
// else entirely.
func TestQuietMinIntervalDefersRatherThanDrops(t *testing.T) {
	dir := t.TempDir()
	cfg := quietCfg(Quiet{MinInterval: 30 * time.Minute})

	// $30 of a $50 budget: past the 50% tier.
	tp := writeTranscript(t, dir, turnLine(ts(1), 60_000_000, opus))
	first := Evaluate(dir, "s", tp, cfg, fixedNow)
	if !first.Nudge || first.FiredFraction != 0.50 {
		t.Fatalf("first nudge = %+v, want the 50%% tier to speak", first)
	}

	// $45: past 75%, one minute later — inside the floor.
	rewrite(t, tp, turnLine(ts(1), 60_000_000, opus), turnLine(ts(2), 30_000_000, opus))
	held := Evaluate(dir, "s", tp, cfg, fixedNow.Add(time.Minute))
	if held.Nudge {
		t.Fatalf("second nudge spoke %q inside a 30m floor", held.Message)
	}
	if held.Suppressed != "min_interval" {
		t.Errorf("Suppressed = %q, want min_interval", held.Suppressed)
	}

	// An hour on, with nothing new to say about spend, the deferred tier
	// still speaks: the floor delayed it, it did not swallow it.
	spoke := Evaluate(dir, "s", tp, cfg, fixedNow.Add(time.Hour))
	if !spoke.Nudge {
		t.Fatal("the deferred 75% tier never spoke; a floor that drops findings is a mute, not a rate limit")
	}
	if spoke.FiredFraction != 0.75 {
		t.Errorf("FiredFraction = %v, want the deferred 0.75 tier", spoke.FiredFraction)
	}
}

// The cap drops. A cap that queues everything it refused is not a cap, so
// a capped finding latches and the session stays quiet.
func TestQuietMaxPerSessionDrops(t *testing.T) {
	dir := t.TempDir()
	cfg := quietCfg(Quiet{MaxPerSession: 1})

	tp := writeTranscript(t, dir, turnLine(ts(1), 60_000_000, opus))
	if first := Evaluate(dir, "s", tp, cfg, fixedNow); !first.Nudge {
		t.Fatal("the first nudge of the session was capped")
	}

	rewrite(t, tp, turnLine(ts(1), 60_000_000, opus), turnLine(ts(2), 30_000_000, opus))
	capped := Evaluate(dir, "s", tp, cfg, fixedNow.Add(2*time.Hour))
	if capped.Nudge {
		t.Fatalf("a second nudge spoke %q under max_per_session: 1", capped.Message)
	}
	if capped.Suppressed != "max_per_session" {
		t.Errorf("Suppressed = %q, want max_per_session", capped.Suppressed)
	}

	// Still quiet much later: the cap is for the session, not a window.
	rewrite(t, tp,
		turnLine(ts(1), 60_000_000, opus), turnLine(ts(2), 30_000_000, opus),
		turnLine(ts(3), 40_000_000, opus))
	if later := Evaluate(dir, "s", tp, cfg, fixedNow.Add(24*time.Hour)); later.Nudge {
		t.Fatalf("a capped session spoke again after a day: %q", later.Message)
	}
}

// The cap is per session. Another session starts with its own allowance.
func TestQuietCapIsPerSession(t *testing.T) {
	dir := t.TempDir()
	cfg := quietCfg(Quiet{MaxPerSession: 1})

	tp := writeTranscript(t, dir, turnLine(ts(1), 60_000_000, opus))
	if d := Evaluate(dir, "a", tp, cfg, fixedNow); !d.Nudge {
		t.Fatal("session a was capped on its first nudge")
	}
	if d := Evaluate(dir, "b", tp, cfg, fixedNow); !d.Nudge {
		t.Fatal("session b inherited session a's cap")
	}
}

// Zero values are the whole point of the default: the hook behaves
// exactly as it did before the key existed, with the per-finding latches
// as the only policy.
func TestQuietZeroIsTheOldBehaviour(t *testing.T) {
	dir := t.TempDir()
	cfg := DefaultConfig()

	tp := writeTranscript(t, dir, turnLine(ts(1), 60_000_000, opus))
	if d := Evaluate(dir, "s", tp, cfg, fixedNow); !d.Nudge || d.Suppressed != "" {
		t.Fatalf("first Stop = %+v, want an unsuppressed nudge", d)
	}
	rewrite(t, tp, turnLine(ts(1), 60_000_000, opus), turnLine(ts(2), 30_000_000, opus))
	d := Evaluate(dir, "s", tp, cfg, fixedNow.Add(time.Second))
	if !d.Nudge || d.Suppressed != "" {
		t.Fatalf("second Stop one second later = %+v, want an unsuppressed nudge with no quiet policy set", d)
	}
}

// A rate limit nobody can see working is indistinguishable from one that
// does nothing — the defect this key was held back for. Every suppression
// lands in the ledger and surfaces in stats.
func TestQuietSuppressionIsVisibleInStats(t *testing.T) {
	dir := t.TempDir()
	cfg := quietCfg(Quiet{MinInterval: time.Hour})

	tp := writeTranscript(t, dir, turnLine(ts(1), 60_000_000, opus))
	Evaluate(dir, "s", tp, cfg, fixedNow)
	rewrite(t, tp, turnLine(ts(1), 60_000_000, opus), turnLine(ts(2), 30_000_000, opus))
	Evaluate(dir, "s", tp, cfg, fixedNow.Add(time.Minute))

	s, err := ReadStats(dir)
	if err != nil {
		t.Fatalf("ReadStats: %v", err)
	}
	if s.Suppressed["min_interval"] != 1 {
		t.Errorf("Suppressed = %v, want one min_interval holdback", s.Suppressed)
	}
	if s.Alerts != 1 {
		t.Errorf("Alerts = %d, want 1 — the held-back nudge is not an alert", s.Alerts)
	}
}

// Delivery observe already silences the coach. The quiet policy must not
// start counting or timestamping nudges that were never spoken, or the
// first nudge after an upgrade arrives already rate-limited.
func TestQuietIgnoresDisabledCoaching(t *testing.T) {
	dir := t.TempDir()
	cfg := quietCfg(Quiet{MaxPerSession: 1})
	cfg.Enabled = false

	tp := writeTranscript(t, dir, turnLine(ts(1), 60_000_000, opus))
	if d := Evaluate(dir, "s", tp, cfg, fixedNow); d.Nudge || d.Suppressed != "" {
		t.Fatalf("observe-level Stop = %+v, want silence recorded as neither nudge nor holdback", d)
	}

	cfg.Enabled = true
	if d := Evaluate(dir, "s", tp, cfg, fixedNow.Add(time.Minute)); !d.Nudge {
		t.Fatal("the first nudge after enabling was capped by a session that had never spoken")
	}
}

// State written before the policy existed carries no nudge timestamp. A
// floor cannot be enforced against a time nobody recorded, so the first
// nudge after an upgrade speaks.
func TestQuietFloorIgnoresUnrecordedHistory(t *testing.T) {
	dir := t.TempDir()
	saveSession(dir, "s", sessionState{CumulativeUSD: 30, LastCountedTS: ts(1)})

	cfg := quietCfg(Quiet{MinInterval: 24 * time.Hour})
	tp := writeTranscript(t, dir, turnLine(ts(2), 1_000_000, opus))
	if d := Evaluate(dir, "s", tp, cfg, fixedNow); !d.Nudge {
		t.Fatalf("a session upgraded mid-flight was silenced by a floor it never recorded: %+v", d)
	}
}
