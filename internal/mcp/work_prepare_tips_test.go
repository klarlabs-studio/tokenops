package mcp

import (
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"testing"

	coachcap "go.klarlabs.de/tokenops/internal/capability/coach"
	ft "go.klarlabs.de/tokenops/internal/contexts/coaching/followthrough"
)

type tipLedger struct{ entries []ft.Entry }

func (l *tipLedger) Append(es ...ft.Entry) error { l.entries = append(l.entries, es...); return nil }
func (l *tipLedger) Load() ([]ft.Entry, error)   { return l.entries, nil }

func prepareTipsOnce(t *testing.T, d RoutingAdviceDeps) prepareWorkResult {
	t.Helper()
	srv := NewServer("tokenops", "test", slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := RegisterRoutingAdviceTools(srv, d); err != nil {
		t.Fatal(err)
	}
	out := execTool(t, srv, "tokenops_prepare_work", map[string]any{
		"instruction": "fix the flaky timing test", "provider": "anthropic", "model": "claude-opus-5",
	})
	if strings.Contains(out, "flaky timing") {
		t.Fatal("the instruction was echoed into the result")
	}
	var prep prepareWorkResult
	if err := json.Unmarshal([]byte(out), &prep); err != nil {
		t.Fatalf("decode: %v\n%s", err, out)
	}
	return prep
}

// prepare_work gives the coach's tip for the work about to start, naming
// the window it is about, records it against the workflow, and does not
// give the same tip again within the cool-down.
func TestPrepareWorkGivesCoachTips(t *testing.T) {
	d := tightWindowDeps(t) // anthropic 5h window at 95%
	l := &tipLedger{}
	d.CoachLedger = func() coachcap.Ledger { return l }
	prep := prepareTipsOnce(t, d)
	var window *coachcap.DecisionTip
	for i, tip := range prep.CoachTips {
		if tip.Object == "anthropic 5h window" {
			window = &prep.CoachTips[i]
		}
		if tip.Point != coachcap.PointPrepareWork || tip.ID == "" || tip.Message == "" {
			t.Errorf("tip = %+v", tip)
		}
	}
	if window == nil {
		t.Fatalf("coach_tips = %+v; want a tip naming the anthropic 5h window", prep.CoachTips)
	}
	var offer *ft.Entry
	for i, e := range l.entries {
		if e.ID == window.ID {
			offer = &l.entries[i]
		}
	}
	if offer == nil || offer.Ref != prep.WorkflowID || offer.Via != ft.ViaAgent || offer.Point != coachcap.PointPrepareWork {
		t.Fatalf("ledger = %+v; want the tip recorded against workflow %s", l.entries, prep.WorkflowID)
	}
	again := prepareTipsOnce(t, d)
	for _, tip := range again.CoachTips {
		if tip.Object == window.Object && tip.Kind == window.Kind {
			t.Fatalf("the same tip was given again within the cool-down: %+v", again.CoachTips)
		}
	}
}

// Quiet verbosity, or the inform power off, gives no tips.
func TestPrepareWorkTipsFollowTheCoach(t *testing.T) {
	for name, set := range map[string]func(d *RoutingAdviceDeps){
		"quiet":      func(d *RoutingAdviceDeps) { d.Config.Coach.Verbosity = "quiet" },
		"inform off": func(d *RoutingAdviceDeps) { d.Config.Coach.Powers = map[string]string{"inform": "off"} },
	} {
		t.Run(name, func(t *testing.T) {
			d := tightWindowDeps(t)
			l := &tipLedger{}
			d.CoachLedger = func() coachcap.Ledger { return l }
			set(&d)
			if prep := prepareTipsOnce(t, d); len(prep.CoachTips) != 0 || len(l.entries) != 0 {
				t.Fatalf("coach_tips = %+v, ledger = %+v; want none", prep.CoachTips, l.entries)
			}
		})
	}
}
