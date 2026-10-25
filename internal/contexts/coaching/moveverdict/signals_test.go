package moveverdict

import (
	"testing"
	"time"
)

func TestObserveCallsReadsEachSignal(t *testing.T) {
	at := func(m int) time.Time { return t0.Add(time.Duration(m) * time.Minute) }
	base := func(ref string, m int) Call {
		return Call{Ref: ref, At: at(m), ResultAt: at(m + 2), HasResult: true, Kind: "edit", Model: "opus",
			Words: "fix the parser bug", Mentions: []string{"internal/parse/parser.go"}}
	}
	now := at(200)
	cases := []struct {
		name string
		tl   Timeline
		want Signals
	}{
		{"a clean run", Timeline{Calls: []Call{base("a", 0)}}, Signals{}},
		{"failed", Timeline{Calls: []Call{func() Call { c := base("a", 0); c.Failed = true; return c }()}}, Signals{Failed: 1}},
		{"interrupted in its unit", Timeline{Calls: []Call{base("a", 0)}, Interrupts: []time.Time{at(5)}}, Signals{Interrupted: 1}},
		{"an interrupt after the next prompt is not its own", Timeline{Calls: []Call{base("a", 0)},
			Prompts: []Prompt{{At: at(10)}}, Interrupts: []time.Time{at(11)}}, Signals{}},
		{"rejected by the next prompt", Timeline{Calls: []Call{base("a", 0)}, Prompts: []Prompt{{At: at(10), Rejects: true}}}, Signals{Rejected: 1}},
		{"the parent reworks a file the subagent named", Timeline{Calls: []Call{base("a", 0)},
			Edits: []Edit{{At: at(4), File: "/repo/internal/parse/parser.go"}}}, Signals{Reworked: 1}},
		{"the parent edits an unrelated file", Timeline{Calls: []Call{base("a", 0)},
			Edits: []Edit{{At: at(4), File: "/repo/README.md"}}}, Signals{}},
		{"an edit before the result is not rework", Timeline{Calls: []Call{base("a", 0)},
			Edits: []Edit{{At: at(1), File: "/repo/internal/parse/parser.go"}}}, Signals{}},
		{"the parent re-runs the same work", Timeline{Calls: []Call{base("a", 0), func() Call {
			c := base("b", 10)
			c.Words = "fix parser bug again"
			return c
		}()}}, Signals{Rerun: 1}},
		{"different work of the same kind is no re-run", Timeline{Calls: []Call{base("a", 0), func() Call {
			c := base("b", 10)
			c.Words = "add a lexer test"
			return c
		}()}}, Signals{}},
		{"a re-run long after is new work", Timeline{Calls: []Call{base("a", 0), base("b", 100)}}, Signals{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			obs, pending := ObserveCalls(tc.tl, nil, now)
			if pending {
				t.Fatalf("pending with every result settled")
			}
			var got *Observation
			for i := range obs {
				if obs[i].Ref == "a" {
					got = &obs[i]
				}
			}
			if got == nil {
				t.Fatalf("no observation for a: %+v", obs)
			}
			if got.Signals != tc.want {
				t.Fatalf("signals = %+v, want %+v", got.Signals, tc.want)
			}
			wantBad := 0
			if tc.want != (Signals{}) {
				wantBad = 1
			}
			if got.Trials != 1 || got.Bad != wantBad {
				t.Errorf("trials/bad = %d/%d, want 1/%d", got.Trials, got.Bad, wantBad)
			}
		})
	}
}

func TestObserveCallsWaitsAndExcludes(t *testing.T) {
	now := t0.Add(10 * time.Minute)
	tl := Timeline{Session: "s", Scope: "p", Calls: []Call{
		{Ref: "running", At: t0, Kind: "edit", Model: "opus"},
		{Ref: "fresh", At: t0, ResultAt: t0.Add(time.Minute), HasResult: true, Kind: "edit", Model: "opus"},
		{Ref: "declined", At: t0, ResultAt: t0.Add(time.Minute), HasResult: true, Excluded: true, Kind: "edit", Model: "opus"},
		{Ref: "unclassified", At: t0.Add(-time.Hour), ResultAt: t0.Add(-time.Hour), HasResult: true, Model: "opus"},
	}}
	obs, pending := ObserveCalls(tl, nil, now)
	if len(obs) != 0 || !pending {
		t.Fatalf("obs = %+v, pending = %v; want none yet, pending", obs, pending)
	}
	// A subagent still running stays pending; the settled one is judged.
	obs, pending = ObserveCalls(tl, nil, now.Add(Settle))
	if !pending || len(obs) != 1 || obs[0].Ref != "fresh" {
		t.Fatalf("after settling: obs = %+v, pending = %v", obs, pending)
	}
	// A call that never returned (the session died) stops being waited on.
	if _, pending = ObserveCalls(tl, nil, t0.Add(MaxRun+time.Minute)); pending {
		t.Fatal("still waiting on a call that never returned")
	}
}

func TestObserveCallsMarksTheCoachsMoves(t *testing.T) {
	tl := Timeline{Session: "s", Scope: "p", Calls: []Call{
		{Ref: "moved", At: t0, ResultAt: t0, HasResult: true, Kind: "edit", Model: "sonnet"},
	}}
	moves := map[string]Move{"moved": {ID: "m1", Kind: "deep", From: "opus", To: "sonnet"}}
	obs, _ := ObserveCalls(tl, moves, t0.Add(Settle))
	if len(obs) != 1 {
		t.Fatalf("obs = %+v", obs)
	}
	o := obs[0]
	// The kind and models are the move's own, as the hook decided them.
	if !o.Moved || o.MoveID != "m1" || o.Kind != "deep" || o.From != "opus" || o.Model != "sonnet" || o.Scope != "p" || o.Source != SourceClaudeTranscript {
		t.Fatalf("moved observation = %+v", o)
	}
}

func TestObserveSessionCountsUnitsUpToTheCap(t *testing.T) {
	units := make([]UnitOutcome, 0, 30)
	for i := 0; i < 30; i++ {
		units = append(units, UnitOutcome{Reworked: i%3 == 0, Interrupted: i == 1, Rejected: i == 1})
	}
	o := ObserveSession("s", "p", t0, "sonnet", units, &Move{ID: "lv", From: "opus", To: "sonnet"})
	if o.Trials != MaxSessionUnits || o.Kind != KindDefaultModel || !o.Moved || o.From != "opus" || o.MoveID != "lv" {
		t.Fatalf("session observation = %+v", o)
	}
	// Units 0,3,…,18 rework (7) and unit 1 is interrupted and rejected.
	if o.Bad != 8 || o.Signals.Reworked != 7 || o.Signals.Interrupted != 1 || o.Signals.Rejected != 1 {
		t.Fatalf("bad = %d, signals = %+v", o.Bad, o.Signals)
	}
	if plain := ObserveSession("s", "p", t0, "opus", units[:2], nil); plain.Moved || plain.Trials != 2 {
		t.Fatalf("unmoved session = %+v", plain)
	}
}

func TestUnitOutcomesReadTheAgentDXSignals(t *testing.T) {
	at := func(m int) time.Time { return t0.Add(time.Duration(m) * time.Minute) }
	tl := Timeline{
		Prompts: []Prompt{{At: at(0)}, {At: at(10)}, {At: at(20), Rejects: true}, {At: at(30)}, {At: at(40)}},
		Edits: []Edit{
			// Unit 0: a.go in two parts is one episode, not rework.
			{At: at(1), File: "a.go"}, {At: at(2), File: "a.go"},
			// Unit 1: back to a.go after b.go is rework.
			{At: at(11), File: "a.go"}, {At: at(12), File: "b.go"}, {At: at(13), File: "a.go"},
		},
		// Unit 3 is interrupted.
		Interrupts: []time.Time{at(35)},
	}
	got := UnitOutcomes(tl, time.Time{})
	want := []UnitOutcome{{}, {Reworked: true, Rejected: true}, {}, {Interrupted: true}, {}}
	if len(got) != len(want) {
		t.Fatalf("units = %+v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("unit %d = %+v, want %+v", i, got[i], want[i])
		}
	}
	// A model switch at minute 25 ends the evidence: units 0-2 only.
	if cut := UnitOutcomes(tl, at(25)); len(cut) != 3 {
		t.Fatalf("units before the switch = %+v", cut)
	}
}
