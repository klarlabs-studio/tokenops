package effortweeks

import (
	"context"
	"math"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/governance/agentdx"
	"go.klarlabs.de/tokenops/internal/contexts/observability/analytics"
	"go.klarlabs.de/tokenops/internal/storage/sqlite"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

var monday = time.Date(2026, 9, 7, 0, 0, 0, 0, time.UTC)

// units writes n one-turn instructions in session sid at effort, from at,
// in Claude Code.
func units(sid, model, effort string, n int, at time.Time) []agentdx.Record {
	out := make([]agentdx.Record, 0, 2*n)
	for i := range n {
		t := at.Add(time.Duration(i) * time.Minute)
		out = append(out,
			agentdx.Record{At: t, SessionID: sid, Kind: agentdx.KindPrompt, Harness: agentdx.SourceClaudeCode},
			agentdx.Record{At: t.Add(10 * time.Second), SessionID: sid, Kind: agentdx.KindAssistantTurn, Harness: agentdx.SourceClaudeCode,
				Model: model, Effort: effort, InputTokens: 1000})
	}
	return out
}

func TestFiguresPoolEachModelsWeek(t *testing.T) {
	recs := units("a", "m", "xhigh", 30, monday.Add(time.Hour))
	recs = append(recs, units("b", "m", "medium", 10, monday.Add(2*time.Hour))...)
	// Outside the week: left out.
	recs = append(recs, units("c", "m", "low", 5, monday.AddDate(0, 0, 8))...)
	turns := []analytics.SessionTurn{}
	for _, r := range recs {
		if r.Kind == agentdx.KindAssistantTurn {
			usd := 1.0
			if r.Effort == "medium" {
				usd = 0.5
			}
			turns = append(turns, analytics.SessionTurn{SessionID: r.SessionID, At: r.At, APIEquivalentUSD: usd})
		}
	}
	w, err := Figures(context.Background(),
		func(context.Context, time.Time, time.Time) ([]agentdx.Record, error) { return recs, nil },
		func(context.Context, time.Time) ([]analytics.SessionTurn, error) { return turns, nil },
		monday)
	if err != nil {
		t.Fatal(err)
	}
	if len(w.Models) != 1 {
		t.Fatalf("models = %+v", w.Models)
	}
	m := w.Models[0]
	if m.Client != "claude-code" || m.Instructions != 40 || m.Levels["xhigh"] != 30 || m.Levels["medium"] != 10 || m.dominant() != "xhigh" {
		t.Errorf("week = %+v", m)
	}
	if want := (30*1.0 + 10*0.5) / 40; math.Abs(m.USDPerInstruction-want) > 1e-9 {
		t.Errorf("cost per instruction = %v, want %v", m.USDPerInstruction, want)
	}
}

type fakeLog struct {
	pos     sqlite.Position
	changed []time.Time
}

func (l *fakeLog) Position(context.Context) (sqlite.Position, error) { return l.pos, nil }
func (l *fakeLog) ChangedTimes(context.Context, eventschema.EventType, sqlite.Position, sqlite.Position) ([]time.Time, bool, error) {
	return l.changed, true, nil
}

// A finished week is computed once; a change in the log recomputes only
// the week it touched.
func TestRefreshComputesAWeekOnce(t *testing.T) {
	dir := t.TempDir()
	log := &fakeLog{pos: sqlite.Position{Rowid: 1, Seq: 1}}
	calls := map[time.Time]int{}
	figures := func(_ context.Context, start time.Time) (Week, error) {
		calls[start]++
		return Week{Start: start}, nil
	}
	now := monday.AddDate(0, 0, 7*Kept).Add(time.Hour)
	r, err := Refresh(context.Background(), log, figures, dir, now)
	if err != nil || len(r.Weeks) != Kept || len(calls) != Kept {
		t.Fatalf("first refresh: %d weeks, %d computed, err %v", len(r.Weeks), len(calls), err)
	}
	if _, err := Refresh(context.Background(), log, figures, dir, now); err != nil {
		t.Fatal(err)
	}
	for w, n := range calls {
		if n != 1 {
			t.Errorf("week %s computed %d times with nothing changed", w.Format(time.DateOnly), n)
		}
	}
	log.pos = sqlite.Position{Rowid: 2, Seq: 2}
	log.changed = []time.Time{monday.AddDate(0, 0, 7*3).Add(time.Hour)}
	if _, err := Refresh(context.Background(), log, figures, dir, now); err != nil {
		t.Fatal(err)
	}
	if calls[monday.AddDate(0, 0, 21)] != 2 || calls[monday.AddDate(0, 0, 14)] != 1 {
		t.Errorf("calls = %v; want only the touched week again", calls)
	}
}

func modelWeek(effort string, usd, firstTry float64) ModelWeek {
	return ModelWeek{Client: "claude-code", Model: "m", Instructions: 50, Levels: map[string]int{effort: 50},
		USDPerInstruction: usd, FirstTryPct: firstTry, RejectedPct: 5, InterruptedPct: 4}
}

func TestWatches(t *testing.T) {
	tests := []struct {
		name      string
		after     ModelWeek
		afterN    int
		judged    bool
		realized  bool
		prevented float64
	}{
		{name: "waiting for weeks after the change", after: modelWeek("medium", 0.5, 70), afterN: 2},
		{name: "realized: cost fell, outcomes held", after: modelWeek("medium", 0.5, 70), afterN: 4, judged: true, realized: true,
			prevented: (1.2 - 0.5) * 50},
		{name: "outcomes worse: not realized", after: modelWeek("medium", 0.5, 55), afterN: 4, judged: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var r Rollup
			for i := range 4 {
				r.Weeks = append(r.Weeks, Week{Start: monday.AddDate(0, 0, 7*i), Models: []ModelWeek{modelWeek("xhigh", 1.2+0.01*float64(i%2), 70)}})
			}
			for i := range tt.afterN {
				r.Weeks = append(r.Weeks, Week{Start: monday.AddDate(0, 0, 7*(4+i)), Models: []ModelWeek{tt.after}})
			}
			ws := r.Watches()
			if len(ws) != 1 {
				t.Fatalf("watches = %+v", ws)
			}
			v := ws[0].Verdict
			if v.Change.From != "xhigh" || v.Change.To != "medium" || v.Judged != tt.judged || v.Realized != tt.realized {
				t.Fatalf("verdict = %+v", v)
			}
			if math.Abs(ws[0].PreventedUSDPerWeek-tt.prevented) > 0.5 {
				t.Errorf("prevented = %v, want about %v", ws[0].PreventedUSDPerWeek, tt.prevented)
			}
		})
	}
}

// No change of level, no watch.
func TestWatchesNeedAChange(t *testing.T) {
	var r Rollup
	for i := range 8 {
		r.Weeks = append(r.Weeks, Week{Start: monday.AddDate(0, 0, 7*i), Models: []ModelWeek{modelWeek("high", 1, 70)}})
	}
	if ws := r.Watches(); len(ws) != 0 {
		t.Fatalf("watches = %+v", ws)
	}
}
