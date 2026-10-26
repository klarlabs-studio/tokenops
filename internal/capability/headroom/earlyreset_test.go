package headroom_test

import (
	"context"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/headroom"
	"go.klarlabs.de/tokenops/internal/contexts/spend/plans"
	"go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/codexappserver"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// A Codex weekly window seen at 62% used, due to reset on the 30th, and
// then reset early: the next reading, an hour later, shows 0% and a fresh
// week.
func earlyResetReadings() []*eventschema.Envelope {
	oldReset := time.Date(2026, 10, 30, 18, 0, 0, 0, time.UTC)
	newReset := time.Date(2026, 11, 1, 9, 30, 0, 0, time.UTC)
	snap := func(used float64, resets time.Time) codexappserver.Snapshot {
		return codexappserver.Snapshot{PlanType: "plus", Secondary: &codexappserver.Window{
			UsedPercent: used, WindowDurationMins: 10080, ResetsAt: resets.Unix(),
		}}
	}
	return []*eventschema.Envelope{
		codexappserver.Envelope(snap(62, oldReset), time.Date(2026, 10, 25, 9, 0, 0, 0, time.UTC)),
		codexappserver.Envelope(snap(0, newReset), time.Date(2026, 10, 25, 10, 0, 0, 0, time.UTC)),
	}
}

// The glance reports a window's early reset for the day after it, and
// carries it on the wire.
func TestGlanceReportsAnEarlyResetForADay(t *testing.T) {
	deps := headroom.Deps{Config: cfgWith(map[string]string{"openai": "gpt-plus"}), Reader: &eventsReader{envs: earlyResetReadings()}}
	noon := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)

	g, err := headroom.ComputeGlance(context.Background(), deps, noon)
	if err != nil {
		t.Fatal(err)
	}
	if len(g.EarlyResets) != 1 || g.EarlyResets[0].Provider != "openai" || g.EarlyResets[0].Name != "week" {
		t.Fatalf("early resets %+v", g.EarlyResets)
	}
	if p := g.Payload(); len(p.EarlyResets) != 1 {
		t.Errorf("payload early resets %+v", p.EarlyResets)
	}

	later := time.Date(2026, 10, 26, 11, 0, 0, 0, time.UTC)
	g, err = headroom.ComputeGlance(context.Background(), deps, later)
	if err != nil {
		t.Fatal(err)
	}
	if len(g.EarlyResets) != 0 {
		t.Errorf("a reset from over a day ago is still shown: %+v", g.EarlyResets)
	}
}

// A switch of plan between the readings is not a reset.
func TestGlanceIgnoresAPlanSwitch(t *testing.T) {
	deps := headroom.Deps{
		Config: cfgWith(map[string]string{"openai": "gpt-pro"}),
		Reader: &eventsReader{envs: earlyResetReadings()},
		PlanHistory: func() (plans.History, error) {
			return plans.History{{Provider: "openai", Plan: "gpt-pro", From: time.Date(2026, 10, 25, 9, 30, 0, 0, time.UTC)}}, nil
		},
	}
	g, err := headroom.ComputeGlance(context.Background(), deps, time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if len(g.EarlyResets) != 0 {
		t.Errorf("a plan switch read as an early reset: %+v", g.EarlyResets)
	}
}
