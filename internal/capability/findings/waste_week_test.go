package findings

import (
	"strings"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/avoidable"
	wastetax "go.klarlabs.de/tokenops/internal/contexts/coaching/waste"
	"go.klarlabs.de/tokenops/internal/contexts/learning"
)

func weekReport(ready bool) avoidable.Report {
	since := time.Date(2026, 10, 7, 9, 0, 0, 0, time.UTC)
	cls := func(c wastetax.Class, usd float64) avoidable.ClassCost {
		return avoidable.ClassCost{Class: c, Name: wastetax.ClassName(c), Amount: avoidable.Amount{Tokens: int64(usd * 1000), APIEquivalentUSD: usd}}
	}
	next := avoidable.Guardrail{Capability: learning.CapabilityReadGuard, Name: "read guard", Stage: learning.StageShadow,
		Next: learning.StageAdvise, Ready: ready, Promote: "tokenops coach set waste advise", Bar: "✗ shadow calls: 12 (needs 50)"}
	return avoidable.Report{
		Since: since, Until: since.Add(7 * 24 * time.Hour),
		Avoidable: avoidable.Amount{Tokens: 12_500, APIEquivalentUSD: 12.5,
			WindowShare: []avoidable.WindowShare{{Provider: "anthropic", Window: "weekly", Pct: 6.25}}},
		Classes: []avoidable.ClassCost{
			cls(wastetax.ClassReread, 2), cls(wastetax.ClassCacheRebuild, 0), cls(wastetax.ClassAbandoned, 8), cls(wastetax.ClassRetryLoop, 2.5),
		},
		Realized: avoidable.Realized{USD: 1.5},
		Next:     &next,
		Headline: "x",
	}
}

// The week's waste is one ambient finding: avoidable in both currencies
// beside realized, the classes that carried it, and the next guardrail.
func TestWasteWeekFinding(t *testing.T) {
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	for _, ready := range []bool{true, false} {
		f := wasteWeek(&avoidable.Snapshot{ComputedAt: now.Add(-time.Hour), Report: weekReport(ready)}, now)
		if len(f) != 1 {
			t.Fatalf("findings = %+v", f)
		}
		got := f[0]
		if got.Kind != KindWaste || got.Level != LevelInfo {
			t.Errorf("finding = %+v", got)
		}
		for _, want := range []string{"Last 7 days: ~$12.50 avoidable", "6.2% of a weekly window", "$1.50 realized"} {
			if !strings.Contains(got.Title, want) {
				t.Errorf("title %q lacks %q", got.Title, want)
			}
		}
		if !strings.Contains(got.Evidence, "abandoned sessions $8.00 · retry loops and runaway growth $2.50 · redundant re-reads $2.00") {
			t.Errorf("evidence = %q", got.Evidence)
		}
		if ready && !strings.Contains(got.Action, "`tokenops coach set waste advise`") {
			t.Errorf("ready action = %q", got.Action)
		}
		if !ready && (!strings.Contains(got.Action, "still gathering evidence") || !strings.Contains(got.Action, "tokenops spend waste")) {
			t.Errorf("not-ready action = %q", got.Action)
		}
	}
}

// Nothing avoidable, no run yet, or a run too old to trust: no finding.
func TestWasteWeekQuiet(t *testing.T) {
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	empty := weekReport(true)
	empty.Avoidable, empty.Headline = avoidable.Amount{}, ""
	for name, s := range map[string]*avoidable.Snapshot{
		"no run":  nil,
		"nothing": {ComputedAt: now, Report: empty},
		"stale":   {ComputedAt: now.Add(-avoidable.MaxSnapshotAge - time.Hour), Report: weekReport(true)},
	} {
		if f := wasteWeek(s, now); len(f) != 0 {
			t.Errorf("%s: %+v", name, f)
		}
	}
}

// Compute carries it.
func TestComputeShowsTheWasteWeek(t *testing.T) {
	r := Compute(Inputs{Waste: &avoidable.Snapshot{ComputedAt: time.Now(), Report: weekReport(true)}})
	for _, f := range r.Findings {
		if f.Kind == KindWaste && strings.HasPrefix(f.Title, "Last 7 days") {
			return
		}
	}
	t.Errorf("findings = %+v", r.Findings)
}
