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
			name: "the same rates on a large sample hold",
			// 200 a side at 20%: the sample could detect a drop of about
			// 11 points, so finding none is evidence the move holds.
			obs:     join(calls("mv", "edit", "sonnet", "p", true, 200, 40, t0), calls("base", "edit", "opus", "p", false, 200, 40, t0)),
			verdict: VerdictHolds,
		},
		{
			name: "the same rates on a small sample are not enough evidence",
			// 10 a side could only detect a drop of about 50 points.
			obs:     join(calls("mv", "edit", "sonnet", "p", true, 10, 2, t0), calls("base", "edit", "opus", "p", false, 10, 2, t0)),
			verdict: VerdictNotEnoughEvidence,
		},
		{
			name: "a small gap is not a drop",
			// 25% vs 20% on 200 a side: five points, under the fifteen
			// the rule needs.
			obs:     join(calls("mv", "edit", "sonnet", "p", true, 200, 50, t0), calls("base", "edit", "opus", "p", false, 200, 40, t0)),
			verdict: VerdictHolds,
		},
		{
			name: "a large gap on noise is not a drop, nor evidence it holds",
			// 3/8 vs 1/8: 25 points, but z is about 1.15, short of 1.645.
			obs:     join(calls("mv", "edit", "sonnet", "p", true, 8, 3, t0), calls("base", "edit", "opus", "p", false, 8, 1, t0)),
			verdict: VerdictNotEnoughEvidence,
		},
		{
			name: "no failures on either side is no evidence on a small sample",
			// 0/8 vs 0/8: a perfect record on eight runs rules out little.
			obs:     join(calls("mv", "edit", "sonnet", "p", true, 8, 0, t0), calls("base", "edit", "opus", "p", false, 8, 0, t0)),
			verdict: VerdictNotEnoughEvidence,
		},
		{
			name:    "moved better than baseline holds on a large sample",
			obs:     join(calls("mv", "edit", "sonnet", "p", true, 200, 0, t0), calls("base", "edit", "opus", "p", false, 200, 50, t0)),
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

// A verdict states the smallest drop its sample could detect, and says
// "not enough evidence yet" with the counts on each side and that size
// whenever the size is larger than the drop the rule retracts at.
func TestJudgeStatesWhatTheSampleCouldDetect(t *testing.T) {
	now := t0.Add(48 * time.Hour)
	cases := []struct {
		name     string
		n, bad   int
		min, max float64
		verdict  Verdict
	}{
		{"eight a side", 8, 1, 0.40, 0.70, VerdictNotEnoughEvidence},
		{"forty a side", 40, 8, 0.20, 0.35, VerdictNotEnoughEvidence},
		{"two hundred a side", 200, 40, 0.08, 0.15, VerdictHolds},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			obs := join(calls("mv", "edit", "sonnet", "p", true, tc.n, tc.bad, t0),
				calls("base", "edit", "opus", "p", false, tc.n, tc.bad, t0))
			b := beliefFor(t, Judge(obs, nil, now), editOpusSonnet)
			if b.Verdict != tc.verdict {
				t.Fatalf("verdict = %s, want %s (%s)", b.Verdict, tc.verdict, b.Why)
			}
			if b.Detectable < tc.min || b.Detectable > tc.max {
				t.Fatalf("detectable drop = %.3f, want within [%.2f, %.2f]", b.Detectable, tc.min, tc.max)
			}
			want := fmt.Sprintf("could detect drops of %d points or more", points(b.Detectable))
			if !strings.Contains(b.Why, want) {
				t.Errorf("why = %q, want it to contain %q", b.Why, want)
			}
			if tc.verdict == VerdictNotEnoughEvidence {
				counts := fmt.Sprintf("%d moved, %d comparable", tc.n, tc.n)
				if !strings.Contains(b.Why, "not enough evidence yet") || !strings.Contains(b.Why, counts) {
					t.Errorf("why = %q, want the counts %q and the verdict named", b.Why, counts)
				}
			}
		})
	}
}

// Instructions within a session are not independent, so the default
// model's power is counted in sessions: many instructions in a few
// sessions are not a large sample.
func TestSessionPowerCountsSessions(t *testing.T) {
	now := t0.Add(48 * time.Hour)
	k := Key{Kind: KindDefaultModel, From: "opus", To: "sonnet"}
	var obs []Observation
	for i := 0; i < 4; i++ {
		obs = append(obs,
			Observation{Ref: fmt.Sprintf("m%d", i), Session: fmt.Sprintf("m%d", i), Scope: "p", At: t0, Kind: KindDefaultModel,
				Model: "sonnet", Moved: true, From: "opus", Trials: 500, Bad: 50},
			Observation{Ref: fmt.Sprintf("b%d", i), Session: fmt.Sprintf("b%d", i), Scope: "p", At: t0, Kind: KindDefaultModel,
				Model: "opus", Trials: 500, Bad: 50})
	}
	b := beliefFor(t, Judge(obs, nil, now), k)
	if b.Verdict != VerdictNotEnoughEvidence {
		t.Fatalf("2000 instructions in 4 sessions a side read as %s (%s)", b.Verdict, b.Why)
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
		calls("new", "edit", "sonnet", "p", false, 200, 20, now.Add(time.Hour)),
		calls("base2", "edit", "opus", "p", false, 200, 20, now.Add(time.Hour)))
	at := now.Add(2 * time.Hour)
	back := Decide(Judge(fresh, active, at), active, at, func() string { return "d4" })
	if len(back) != 1 || back[0].Reinstate == nil || back[0].Reinstate.EndedBy != EndedByEvidence {
		t.Fatalf("new evidence did not reinstate: %+v", back)
	}
	// A few good runs since are not evidence enough to bring it back.
	few := join(obs,
		calls("new", "edit", "sonnet", "p", false, 10, 1, now.Add(time.Hour)),
		calls("base2", "edit", "opus", "p", false, 10, 1, now.Add(time.Hour)))
	if early := Decide(Judge(few, active, at), active, at, func() string { return "d5" }); len(early) != 0 {
		t.Fatalf("an underpowered sample reinstated the move: %+v", early)
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
