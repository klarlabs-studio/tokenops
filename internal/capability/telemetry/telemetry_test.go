package telemetry

import (
	"strings"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/commits"
	"go.klarlabs.de/tokenops/internal/capability/findings"
	"go.klarlabs.de/tokenops/internal/capability/headroom"
	"go.klarlabs.de/tokenops/internal/capability/sessions"
	"go.klarlabs.de/tokenops/internal/capability/spending"
	"go.klarlabs.de/tokenops/internal/contexts/governance/agentdx"
	"go.klarlabs.de/tokenops/internal/contexts/spend/plans"
	"go.klarlabs.de/tokenops/internal/otlp"
)

func byName(gs []otlp.Gauge) map[string]otlp.Gauge {
	out := map[string]otlp.Gauge{}
	for _, g := range gs {
		out[g.Name] = g
	}
	return out
}

func TestGauges(t *testing.T) {
	var g headroom.Glance
	g.Headroom.Reports = []plans.HeadroomReport{{Provider: "openai", PlanName: "gpt-pro-5x", Windows: []plans.VendorWindow{
		{Name: "week", UsedPct: 75, Pace: &plans.WindowPace{Status: plans.PaceAhead, DeltaPct: 55, RunsOutIn: 11 * time.Hour}},
	}}}
	dx := sessions.DX{}
	dx.Metrics.Prompts = 10
	dx.Metrics.FirstTryRatePct = 90
	dx.Grades.Overall = agentdx.LetterA
	got := byName(Gauges(Inputs{
		Glance:   &g,
		Findings: &findings.Report{Findings: []findings.Finding{{Level: "warn", Kind: "quota"}, {Level: "warn", Kind: "quota"}}},
		Costs:    map[string]spending.ProviderCost{"openai": {Today: spending.Usage{Tokens: 100, APIEquivalentUSD: 2, Requests: 10, UnpricedRequests: 1}}},
		DX:       &dx,
		Commits:  &commits.Report{CommitsTotal: 3, MedianUSD: 0.7, Commits: []commits.CommitCost{{Subject: "secret plans"}}},
	}))
	check := func(name string, attrs map[string]string, want float64) {
		t.Helper()
		for _, p := range got[name].Points {
			match := true
			for k, v := range attrs {
				if p.Attrs[k] != v {
					match = false
				}
			}
			if match {
				if p.Value != want {
					t.Errorf("%s %v = %v, want %v", name, attrs, p.Value, want)
				}
				return
			}
		}
		t.Errorf("%s %v missing", name, attrs)
	}
	check("tokenops.plan.window.utilization", map[string]string{"provider": "openai", "window": "week"}, 75)
	check("tokenops.plan.window.pace", map[string]string{"window": "week"}, 55)
	check("tokenops.plan.window.runs_out_in", map[string]string{"window": "week"}, 11*3600)
	check("tokenops.usage.api_equivalent", map[string]string{"provider": "openai", "period": "24h"}, 2)
	check("tokenops.usage.unpriced_share", map[string]string{"period": "24h"}, 10)
	check("tokenops.sessions.dx", map[string]string{"measure": "first_try_rate_pct"}, 90)
	check("tokenops.sessions.grade", map[string]string{"dimension": "overall"}, 4)
	check("tokenops.coach.findings", map[string]string{"level": "warn", "kind": "quota"}, 2)
	check("tokenops.commits.cost", map[string]string{"statistic": "median"}, 0.7)
	for _, gauge := range got {
		for _, p := range gauge.Points {
			for _, v := range p.Attrs {
				if strings.Contains(v, "secret") {
					t.Errorf("%s carries a commit subject", gauge.Name)
				}
			}
		}
	}
	if len(Gauges(Inputs{})) != 0 {
		t.Error("no readings made gauges")
	}
}
