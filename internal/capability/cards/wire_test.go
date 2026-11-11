package cards

import (
	"encoding/json"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/headroom"
	"go.klarlabs.de/tokenops/internal/contexts/spend/plans"
)

// overWire is g as a client that asked the daemon gets it: its payload,
// sent as JSON and read back.
func overWire(t *testing.T, g headroom.Glance) headroom.Glance {
	t.Helper()
	b, err := json.Marshal(g.Payload())
	if err != nil {
		t.Fatal(err)
	}
	var p headroom.GlancePayload
	if err := json.Unmarshal(b, &p); err != nil {
		t.Fatal(err)
	}
	return p.Glance()
}

// The cards drawn from the daemon's answer are the cards drawn from the
// glance computed here: the CLI asks the daemon and draws what it gets.
func TestCardsFromTheDaemonsAnswerAreTheSame(t *testing.T) {
	now := time.Date(2026, 11, 11, 9, 30, 0, 0, time.UTC)
	full := sample()
	util, cov := 62.0, 90.0
	full.PlanUse = &plans.PlanUseReport{Providers: []plans.PlanUse{{
		Provider: "anthropic", UtilizationPct: &util, UtilizationWindow: "weekly", CoveragePct: &cov,
		Windows: []plans.WindowUse{{Label: "weekly", Windows: 1, AvgPeakPct: util}}}}}
	full.Headroom.Notes = []string{"plan \"claude-mystery\" is not one TokenOps knows"}
	full.Headroom.Reports[0].Windows[0].Stale = true
	full.Headroom.Reports[0].Windows[0].ObservedAt = now.Add(-20 * time.Hour)
	var unconfigured, storageless headroom.Glance
	unconfigured.Headroom.Unconfigured = "bind a plan"
	unconfigured.Budgets.Unconfigured = "bind a plan"
	storageless.Headroom.StorageDisabled = "storage is off"
	for name, g := range map[string]headroom.Glance{"plans": full, "unconfigured": unconfigured, "no storage": storageless} {
		for _, brief := range []bool{false, true} {
			opt := Options{Width: 130, Brief: brief, Now: now, Costs: costs}
			want, got := Render(g, opt), Render(overWire(t, g), opt)
			if got != want {
				t.Errorf("%s, brief=%v: drawn from the daemon's answer\n%s\nwant\n%s", name, brief, got, want)
			}
		}
	}
}
