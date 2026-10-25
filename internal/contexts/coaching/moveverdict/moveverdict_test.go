package moveverdict

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

var t0 = time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)

// calls builds n subagent observations of kind on model, bad of them
// with a failure, in scope, one minute apart from start.
func calls(prefix, kind, model, scope string, moved bool, n, bad int, start time.Time) []Observation {
	out := make([]Observation, 0, n)
	for i := 0; i < n; i++ {
		o := Observation{
			Ref: fmt.Sprintf("%s-%d", prefix, i), Session: "s-" + prefix, Scope: scope, Source: SourceClaudeTranscript,
			At: start.Add(time.Duration(i) * time.Minute), Kind: kind, Model: model, Trials: 1,
		}
		if moved {
			o.Moved, o.From, o.MoveID = true, "opus", "m-"+o.Ref
		}
		if i < bad {
			o.Bad, o.Signals.Failed = 1, 1
		}
		out = append(out, o)
	}
	return out
}

func join(groups ...[]Observation) []Observation {
	var out []Observation
	for _, g := range groups {
		out = append(out, g...)
	}
	return out
}

func beliefFor(t *testing.T, bs []Belief, k Key) Belief {
	t.Helper()
	for _, b := range bs {
		if b.Key == k {
			return b
		}
	}
	t.Fatalf("no belief for %v in %+v", k, bs)
	return Belief{}
}

var editOpusSonnet = Key{Kind: "edit", From: "opus", To: "sonnet"}

func TestJudgeUpdatesTheBeliefFromOutcomes(t *testing.T) {
	now := t0.Add(48 * time.Hour)
	cases := []struct {
		name    string
		obs     []Observation
		verdict Verdict
	}{
		{
			name:    "a clear drop retracts",
			obs:     join(calls("mv", "edit", "sonnet", "p", true, 10, 6, t0), calls("base", "edit", "opus", "p", false, 10, 1, t0)),
			verdict: VerdictDropped,
		},
		{
			name:    "the same rates hold",
			obs:     join(calls("mv", "edit", "sonnet", "p", true, 10, 2, t0), calls("base", "edit", "opus", "p", false, 10, 2, t0)),
			verdict: VerdictHolds,
		},
		{
			name: "a small gap is not a drop",
			// 30% vs 20%: ten points, under the fifteen the rule needs.
			obs:     join(calls("mv", "edit", "sonnet", "p", true, 10, 3, t0), calls("base", "edit", "opus", "p", false, 10, 2, t0)),
			verdict: VerdictHolds,
		},
		{
			name: "a large gap on noise is not a drop",
			// 3/8 vs 1/8: 25 points, but z is about 1.15, short of 1.645.
			obs:     join(calls("mv", "edit", "sonnet", "p", true, 8, 3, t0), calls("base", "edit", "opus", "p", false, 8, 1, t0)),
			verdict: VerdictHolds,
		},
		{
			name:    "moved better than baseline holds",
			obs:     join(calls("mv", "edit", "sonnet", "p", true, 10, 0, t0), calls("base", "edit", "opus", "p", false, 10, 5, t0)),
			verdict: VerdictHolds,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := beliefFor(t, Judge(tc.obs, nil, now), editOpusSonnet)
			if b.Verdict != tc.verdict {
				t.Fatalf("verdict = %s, want %s (%s)", b.Verdict, tc.verdict, b.Why)
			}
			if b.Why == "" || b.Scope == "" {
				t.Errorf("belief carries no reason or scope: %+v", b)
			}
		})
	}
}

func TestJudgeNeedsAMinimumSample(t *testing.T) {
	now := t0.Add(48 * time.Hour)
	cases := []struct {
		name          string
		moved, mbad   int
		base, basebad int
	}{
		{"too few moved runs", 7, 7, 20, 0},
		{"too few comparable runs", 20, 20, 7, 0},
		{"no comparable runs at all", 20, 20, 0, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			obs := join(calls("mv", "edit", "sonnet", "p", true, tc.moved, tc.mbad, t0),
				calls("base", "edit", "opus", "p", false, tc.base, tc.basebad, t0))
			b := beliefFor(t, Judge(obs, nil, now), editOpusSonnet)
			if b.Verdict != VerdictUnjudged {
				t.Fatalf("verdict = %s on a short sample (%+v)", b.Verdict, b)
			}
			if !strings.Contains(b.Why, "at least") {
				t.Errorf("why does not name the minimum: %q", b.Why)
			}
		})
	}
}

func TestJudgeComparesOnlyLikeWithLike(t *testing.T) {
	now := t0.Add(30 * 24 * time.Hour)
	moved := calls("mv", "edit", "sonnet", "p", true, 10, 8, t0.Add(20*24*time.Hour))
	cases := []struct {
		name string
		base []Observation
	}{
		{"a different task kind", calls("base", "research", "opus", "p", false, 20, 0, t0.Add(20*24*time.Hour))},
		{"a different project", calls("base", "edit", "opus", "other", false, 20, 0, t0.Add(20*24*time.Hour))},
		{"a different model than the move came from", calls("base", "edit", "haiku", "p", false, 20, 0, t0.Add(20*24*time.Hour))},
		{"runs weeks apart", calls("base", "edit", "opus", "p", false, 20, 0, t0.Add(9*24*time.Hour))},
		{"other moved runs", calls("base", "edit", "opus", "p", true, 20, 0, t0.Add(20*24*time.Hour))},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := beliefFor(t, Judge(join(moved, tc.base), nil, now), editOpusSonnet)
			if b.Verdict != VerdictUnjudged || b.Baseline != 0 {
				t.Fatalf("compared against unlike runs: %+v", b)
			}
		})
	}
}

func TestJudgeForgetsOutsideTheLookback(t *testing.T) {
	obs := join(calls("mv", "edit", "sonnet", "p", true, 10, 6, t0), calls("base", "edit", "opus", "p", false, 10, 1, t0))
	if bs := Judge(obs, nil, t0.Add(Lookback+time.Hour)); len(bs) != 0 {
		t.Fatalf("judged evidence older than the lookback: %+v", bs)
	}
}

func TestSessionsNeedMoreUnitsAndSessions(t *testing.T) {
	now := t0.Add(48 * time.Hour)
	session := func(ref, model string, moved bool, trials, bad int, at time.Time) Observation {
		o := Observation{Ref: ref, Session: ref, Scope: "p", At: at, Kind: KindDefaultModel, Model: model, Trials: trials, Bad: bad}
		if moved {
			o.Moved, o.From = true, "opus"
		}
		return o
	}
	k := Key{Kind: KindDefaultModel, From: "opus", To: "sonnet"}
	// Three sessions a side: units enough, sessions not.
	three := make([]Observation, 0, 8)
	three = append(three,
		session("m1", "sonnet", true, 10, 6, t0), session("m2", "sonnet", true, 10, 6, t0), session("m3", "sonnet", true, 10, 6, t0),
		session("b1", "opus", false, 10, 1, t0), session("b2", "opus", false, 10, 1, t0), session("b3", "opus", false, 10, 1, t0),
	)
	if b := beliefFor(t, Judge(three, nil, now), k); b.Verdict != VerdictUnjudged {
		t.Fatalf("judged sessions on three a side: %+v", b)
	}
	four := append(three, session("m4", "sonnet", true, 10, 6, t0), session("b4", "opus", false, 10, 1, t0))
	if b := beliefFor(t, Judge(four, nil, now), k); b.Verdict != VerdictDropped {
		t.Fatalf("four sessions a side with a clear drop: %+v", b)
	}
}

func TestDecideRetractsOnceAndReinstatesOnNewEvidence(t *testing.T) {
	now := t0.Add(48 * time.Hour)
	obs := join(calls("mv", "edit", "sonnet", "p", true, 10, 6, t0), calls("base", "edit", "opus", "p", false, 10, 1, t0))
	bs := Judge(obs, nil, now)
	ch := Decide(bs, nil, now, func() string { return "d1" })
	if len(ch) != 1 || ch[0].Retract == nil {
		t.Fatalf("changes = %+v", ch)
	}
	r := *ch[0].Retract
	if r.Key != editOpusSonnet || r.ID != "d1" || r.Reason == "" || len(r.Alternatives) < 2 {
		t.Fatalf("retraction = %+v", r)
	}
	chosen := 0
	for _, a := range r.Alternatives {
		if a.Chosen {
			chosen++
		}
		if a.Why == "" {
			t.Errorf("alternative without a reason: %+v", a)
		}
	}
	if chosen != 1 {
		t.Errorf("want exactly one chosen alternative, got %d", chosen)
	}
	active := []Retraction{r}
	// Judged again on the same evidence: already retracted, no change.
	if again := Decide(Judge(obs, active, now), active, now, func() string { return "d2" }); len(again) != 0 {
		t.Fatalf("retracted twice: %+v", again)
	}
	// The old moved runs age out: no evidence is not evidence of
	// recovery, so the retraction stays.
	later := now.Add(Lookback)
	if aged := Decide(Judge(obs, active, later), active, later, func() string { return "d3" }); len(aged) != 0 {
		t.Fatalf("absence of evidence reinstated the move: %+v", aged)
	}
	// New evidence: runs of the kind on the cheaper model since the
	// retraction (the agent asked for it itself), as good as baseline.
	fresh := join(obs,
		calls("new", "edit", "sonnet", "p", false, 10, 1, now.Add(time.Hour)),
		calls("base2", "edit", "opus", "p", false, 10, 1, now.Add(time.Hour)))
	at := now.Add(2 * time.Hour)
	back := Decide(Judge(fresh, active, at), active, at, func() string { return "d4" })
	if len(back) != 1 || back[0].Reinstate == nil || back[0].Reinstate.EndedBy != EndedByEvidence {
		t.Fatalf("new evidence did not reinstate: %+v", back)
	}
}

func TestLinesExplainEachMoveKind(t *testing.T) {
	now := t0.Add(48 * time.Hour)
	obs := join(
		calls("mv", "edit", "sonnet", "p", true, 10, 6, t0), calls("base", "edit", "opus", "p", false, 10, 1, t0),
		calls("rmv", "research", "haiku", "p", true, 3, 0, t0),
	)
	bs := Judge(obs, nil, now)
	ch := Decide(bs, nil, now, func() string { return "d1" })
	text := strings.Join(Lines(bs, []Retraction{*ch[0].Retract}), "\n")
	for _, want := range []string{
		"edit work, opus → sonnet: 10 moves, 10 judged against 10 comparable runs",
		"60% had a bad outcome vs 10%",
		"quality dropped",
		"retracted",
		"tokenops coach reinstate edit",
		"research work, opus → haiku: 3 moves",
		"not judged yet",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("explain text lacks %q:\n%s", want, text)
		}
	}
}
