package coach

import (
	"strings"
	"testing"
	"time"

	ft "go.klarlabs.de/tokenops/internal/contexts/coaching/followthrough"
)

func TestPrepareWorkTips(t *testing.T) {
	cases := []struct {
		name  string
		in    PrepareTipInput
		kinds []string
		objs  []string
		says  []string
	}{
		{"nothing to say", PrepareTipInput{Recommendation: "stay", Model: "claude-opus-5", CurrentModel: "claude-opus-5", ShareAction: "proceed", ShareWindow: "anthropic 5h window"}, nil, nil, nil},
		{"a task that may not fit names the window",
			PrepareTipInput{ShareAction: "lighter_tier", ShareWindow: "anthropic 5h window", ShareReason: "18% is left and tasks like this take 15–30%."},
			[]string{TipWindowFit}, []string{"anthropic 5h window"}, []string{"may not fit the anthropic 5h window: 18% is left"}},
		{"a nearly used window", PrepareTipInput{Windows: []PrepareWindow{{Name: "Claude week window", UsedPct: 93, ResetsIn: "1d 2h"}}},
			[]string{TipWindowLow}, []string{"Claude week window"}, []string{"Claude week window is 93% used (resets in 1d 2h)"}},
		{"a window running out before its reset", PrepareTipInput{Windows: []PrepareWindow{{Name: "Claude 5h window", UsedPct: 40, RunsOutIn: 95 * time.Minute}}},
			[]string{TipWindowLow}, []string{"Claude 5h window"}, []string{"runs out in 1h35m at the pace so far"}},
		{"a window with room says nothing", PrepareTipInput{Windows: []PrepareWindow{{Name: "Claude 5h window", UsedPct: 40}}}, nil, nil, nil},
		{"one window is named once", PrepareTipInput{ShareAction: "defer", ShareWindow: "Claude 5h window", ShareReason: "r",
			Windows: []PrepareWindow{{Name: "Claude 5h window", UsedPct: 95}}}, []string{TipWindowFit}, []string{"Claude 5h window"}, nil},
		{"a switch names the model", PrepareTipInput{Recommendation: "switch", Model: "claude-haiku-4-5", CurrentModel: "claude-opus-5", Reason: "mechanical work while the window is tight"},
			[]string{TipModelFit}, []string{"claude-haiku-4-5"}, []string{"run this task on claude-haiku-4-5 rather than claude-opus-5: mechanical work"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := PrepareWorkTips(c.in)
			if len(got) != len(c.kinds) {
				t.Fatalf("tips = %+v; want kinds %v", got, c.kinds)
			}
			for i, tip := range got {
				if tip.Kind != c.kinds[i] || tip.Object != c.objs[i] || tip.Point != PointPrepareWork {
					t.Errorf("tip %d = %+v; want %s on %s", i, tip, c.kinds[i], c.objs[i])
				}
				if i < len(c.says) && !strings.Contains(tip.Message, c.says[i]) {
					t.Errorf("tip %d says %q; want %q", i, tip.Message, c.says[i])
				}
			}
		})
	}
}

func TestSettleWorkflowTips(t *testing.T) {
	now := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name     string
		models   map[string]int
		settled  bool
		followed bool
		instead  string
	}{
		{"no measured steps settles nothing", nil, false, false, ""},
		{"mostly on the named model follows it", map[string]int{"claude-haiku-4-5-20251001": 3, "claude-opus-5": 1}, true, true, ""},
		{"mostly elsewhere ignores it", map[string]int{"claude-opus-5": 3, "claude-haiku-4-5": 1}, true, false, "ran on claude-opus-5"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			l := &memLedger{}
			RecordTipAt(l, now.Add(-time.Hour), "t1", "", TipModelFit, PointPrepareWork, "claude-haiku-4-5", "workflow:1")
			RecordTipAt(l, now.Add(-time.Hour), "t2", "", TipModelFit, PointPrepareWork, "claude-haiku-4-5", "workflow:2")
			SettleWorkflowTips(l, now, "workflow:1", c.models)
			SettleWorkflowTips(l, now, "workflow:1", c.models) // settles once
			var resolves []ft.Entry
			for _, e := range l.entries {
				if e.Type == ft.EntryResolve {
					resolves = append(resolves, e)
				}
			}
			if !c.settled {
				if len(resolves) != 0 {
					t.Fatalf("resolves = %+v", resolves)
				}
				return
			}
			if len(resolves) != 1 || resolves[0].ID != "t1" || (resolves[0].Outcome == ft.OutcomeFollowed) != c.followed || resolves[0].Instead != c.instead {
				t.Fatalf("resolves = %+v; want t1 followed=%v instead %q", resolves, c.followed, c.instead)
			}
		})
	}
}
