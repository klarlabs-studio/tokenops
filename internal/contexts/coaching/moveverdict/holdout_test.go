package moveverdict

import (
	"strings"
	"testing"
	"time"
)

// held builds n runs the coach would have moved to "sonnet" and held on
// "opus" at random, bad of them with a failure.
func held(prefix, scope string, n, bad int, start time.Time) []Observation {
	out := calls(prefix, "edit", "opus", scope, false, n, bad, start)
	for i := range out {
		out[i].Held, out[i].From, out[i].HeldFor = true, "opus", "sonnet"
	}
	return out
}

// With enough randomly held runs a move kind is judged against them, not
// against whatever unmoved work happened nearby: the observational
// baseline here is spotless, the randomized one fails as often as the
// moved runs, and the move holds.
func TestJudgeComparesMovesWithTheRandomizedHoldout(t *testing.T) {
	now := t0.Add(48 * time.Hour)
	obs := join(
		calls("moved", "edit", "sonnet", "proj", true, 10, 5, t0),
		calls("nearby", "edit", "opus", "proj", false, 10, 0, t0),
		held("held", "other-proj", 8, 4, t0),
	)
	b := beliefFor(t, Judge(obs, nil, now), editOpusSonnet)
	if !b.Randomized || b.Verdict != VerdictHolds {
		t.Fatalf("belief = %+v, want a randomized comparison that holds", b)
	}
	if b.Baseline != 8 || b.BaselineBad != 4 || b.Judged != 10 {
		t.Errorf("compared %d/%d against %d/%d", b.Bad, b.Judged, b.BaselineBad, b.Baseline)
	}
	if !strings.Contains(b.Scope, "held back at random") {
		t.Errorf("scope = %q", b.Scope)
	}
}

// A randomized drop retracts like an observational one.
func TestJudgeRetractsOnARandomizedDrop(t *testing.T) {
	now := t0.Add(48 * time.Hour)
	obs := join(calls("moved", "edit", "sonnet", "proj", true, 10, 7, t0), held("held", "proj", 10, 1, t0))
	if b := beliefFor(t, Judge(obs, nil, now), editOpusSonnet); !b.Randomized || b.Verdict != VerdictDropped {
		t.Fatalf("belief = %+v", b)
	}
}

// Too few held runs fall back to the observational comparison.
func TestJudgeFallsBackWithoutEnoughHeldRuns(t *testing.T) {
	now := t0.Add(48 * time.Hour)
	obs := join(
		calls("moved", "edit", "sonnet", "proj", true, 10, 0, t0),
		calls("nearby", "edit", "opus", "proj", false, 10, 0, t0),
		held("held", "proj", 3, 3, t0),
	)
	b := beliefFor(t, Judge(obs, nil, now), editOpusSonnet)
	if b.Randomized {
		t.Fatalf("judged on %d held runs: %+v", 3, b)
	}
}

// A held run is evidence for the move it was held from, and is not
// itself a move.
func TestObserveCallsMarksHeldRuns(t *testing.T) {
	tl := Timeline{Session: "s", Scope: "proj", Calls: []Call{{
		Ref: "toolu_1", At: t0, Kind: "edit", Model: "opus", HasResult: true, ResultAt: t0.Add(time.Minute),
	}}}
	obs, _ := ObserveCalls(tl, map[string]Move{"toolu_1": {ID: "h1", Kind: "edit", From: "opus", To: "sonnet", Held: true}}, t0.Add(time.Hour))
	if len(obs) != 1 || !obs[0].Held || obs[0].Moved || obs[0].HeldFor != "sonnet" || obs[0].Model != "opus" || obs[0].From != "opus" {
		t.Fatalf("obs = %+v", obs)
	}
}
