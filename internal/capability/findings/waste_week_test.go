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

// A finding for one week: the plan reading where one is set, the
// classes, and the next guardrail.
func weekFinding(t *testing.T, r avoidable.Report) Finding {
	t.Helper()
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	f := wasteWeek(&avoidable.Snapshot{ComputedAt: now.Add(-time.Hour), Report: r}, now)
	if len(f) != 1 {
		t.Fatalf("findings = %+v", f)
	}
	if f[0].Kind != KindWaste || f[0].Level != LevelInfo || f[0].Key != "waste:week" {
		t.Errorf("finding = %+v", f[0])
	}
	return f[0]
}

// withClassShares gives each class its share of the weekly window, at
// 2,000 tokens per percent as the total's 6.25% has it.
func withClassShares(r avoidable.Report) avoidable.Report {
	for i, c := range r.Classes {
		if c.Tokens > 0 {
			r.Classes[i].WindowShare = []avoidable.WindowShare{{Provider: "anthropic", Window: "weekly", Pct: float64(c.Tokens) / 2_000}}
		}
	}
	return r
}

// On a flat-rate plan the week's waste is a share of a window: the
// dollars are what the usage would cost at API prices, which nobody paid,
// so they lead nothing. Each class is its share of the same window.
func TestWasteWeekOnAPlanLeadsWithTheWindowShare(t *testing.T) {
	for _, ready := range []bool{true, false} {
		got := weekFinding(t, withClassShares(weekReport(ready)))
		if got.Title != "Last 7 days: ~6.2% of a weekly window avoidable" {
			t.Errorf("title = %q", got.Title)
		}
		if !strings.HasPrefix(got.Evidence, "abandoned sessions 4.0% · retry loops and runaway growth 1.2% · redundant re-reads 1.0%") {
			t.Errorf("evidence = %q", got.Evidence)
		}
		// What the guardrails saved is said in words, at API prices.
		if !strings.Contains(got.Evidence, "Guardrails already saved ~$1.50 at API prices") || strings.Contains(got.Title, "realized") {
			t.Errorf("realized: title %q, evidence %q", got.Title, got.Evidence)
		}
		if ready && !strings.Contains(got.Action, "`tokenops coach set waste advise`") {
			t.Errorf("ready action = %q", got.Action)
		}
		if !ready && (!strings.Contains(got.Action, "still gathering evidence") || !strings.Contains(got.Action, "tokenops spend waste")) {
			t.Errorf("not-ready action = %q", got.Action)
		}
	}
}

// Nothing saved yet says nothing about it: "$0.00 realized" read as a
// figure that had gone wrong.
func TestWasteWeekLeavesOutNothingSaved(t *testing.T) {
	r := withClassShares(weekReport(true))
	r.Realized = avoidable.Realized{}
	got := weekFinding(t, r)
	if strings.Contains(got.Title+got.Evidence, "realized") || strings.Contains(got.Title+got.Evidence, "saved") {
		t.Errorf("finding = %+v", got)
	}
}

// Where a provider bills per use (an API key, pay as you go), the
// avoidable part is money spent and leads in dollars, class by class.
func TestWasteWeekBilledPerUseLeadsWithDollars(t *testing.T) {
	r := weekReport(true)
	r.Avoidable.WindowShare = nil
	r.Avoidable.BilledUSD = 12.5
	for i := range r.Classes {
		r.Classes[i].BilledUSD = r.Classes[i].APIEquivalentUSD
	}
	got := weekFinding(t, r)
	if got.Title != "Last 7 days: ~$12.50 avoidable" {
		t.Errorf("title = %q", got.Title)
	}
	if !strings.HasPrefix(got.Evidence, "abandoned sessions $8.00 · retry loops and runaway growth $2.50 · redundant re-reads $2.00") {
		t.Errorf("evidence = %q", got.Evidence)
	}
}

// A plan and a billed provider together: the plan's share leads and the
// billed dollars follow it.
func TestWasteWeekPlanAndBilled(t *testing.T) {
	r := withClassShares(weekReport(true))
	r.Avoidable.BilledUSD = 4
	got := weekFinding(t, r)
	if got.Title != "Last 7 days: ~6.2% of a weekly window avoidable, plus $4.00 billed per use" {
		t.Errorf("title = %q", got.Title)
	}
}

// Neither a calibrated plan nor a bill: the dollars are all there is,
// and they say they are API prices.
func TestWasteWeekWithoutAPlanReading(t *testing.T) {
	r := weekReport(true)
	r.Avoidable.WindowShare = nil
	got := weekFinding(t, r)
	if got.Title != "Last 7 days: ~$12.50 avoidable at API prices" {
		t.Errorf("title = %q", got.Title)
	}
	if !strings.HasPrefix(got.Evidence, "abandoned sessions $8.00 · ") {
		t.Errorf("evidence = %q", got.Evidence)
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

// withRebuilds gives the week cache rebuilds split by cause.
func withRebuilds(r avoidable.Report, causes ...avoidable.CauseCost) avoidable.Report {
	for i, c := range r.Classes {
		if c.Class == wastetax.ClassCacheRebuild {
			var usd float64
			var tokens int64
			for _, x := range causes {
				usd += x.APIEquivalentUSD
				tokens += x.Tokens
			}
			r.Classes[i].Causes = causes
			r.Classes[i].APIEquivalentUSD, r.Classes[i].Tokens = usd, tokens
		}
	}
	return r
}

func cause(c wastetax.RebuildCause, usd float64, ttl string) avoidable.CauseCost {
	return avoidable.CauseCost{Cause: c, Name: wastetax.RebuildCauseName(c), TTL: ttl, Turns: 1,
		Amount: avoidable.Amount{Tokens: int64(usd * 1000), APIEquivalentUSD: usd}}
}

// The finding names what most cache rebuilds came after, in words the
// operator can act on.
func TestWasteWeekNamesTheTopRebuildCause(t *testing.T) {
	cases := []struct {
		name   string
		causes []avoidable.CauseCost
		want   string
	}{
		{"idle gaps on the five-minute cache", []avoidable.CauseCost{cause(wastetax.CauseTTLExpired, 3, "5m"), cause(wastetax.CausePrefixChanged, 1, "")},
			"Most cache rebuilds came after idle gaps over 5 minutes."},
		{"idle gaps on the hour cache", []avoidable.CauseCost{cause(wastetax.CauseTTLExpired, 3, "1h")},
			"Most cache rebuilds came after idle gaps over an hour."},
		{"no majority", []avoidable.CauseCost{cause(wastetax.CauseModelSwitch, 2, ""), cause(wastetax.CauseTTLExpired, 1.5, "5m"), cause(wastetax.CausePrefixChanged, 1.5, "")},
			"Cache rebuilds came most often after model switches."},
		{"prefix changes", []avoidable.CauseCost{cause(wastetax.CausePrefixChanged, 3, "")},
			"Most cache rebuilds came after changes early in the prompt (a timestamp, the tool set)."},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := weekFinding(t, withRebuilds(weekReport(false), c.causes...))
			if !strings.Contains(f.Evidence, c.want) {
				t.Errorf("evidence %q lacks %q", f.Evidence, c.want)
			}
		})
	}
	// Unexplained rebuilds name no cause; no rebuilds, no sentence.
	for _, r := range []avoidable.Report{withRebuilds(weekReport(false), cause(wastetax.CauseUnknown, 3, "")), weekReport(false)} {
		if f := weekFinding(t, r); strings.Contains(f.Evidence, "cache rebuilds") || strings.Contains(f.Evidence, "Cache rebuilds") {
			t.Errorf("a cause named without one: %q", f.Evidence)
		}
	}
}
