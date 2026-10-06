// Package telemetry turns what TokenOps knows into OTLP gauges for a
// collector a company already runs: each plan window's share and pace,
// usage and its value, how sessions go, the coach's findings, and what
// commits cost. Derived figures only — never an event, a prompt, a
// transcript, a file or a commit subject (ADR 0004; the 2026-09-15
// decision to upload derived metrics only).
package telemetry

import (
	"context"
	"time"

	coachcap "go.klarlabs.de/tokenops/internal/capability/coach"
	"go.klarlabs.de/tokenops/internal/capability/commits"
	"go.klarlabs.de/tokenops/internal/capability/findings"
	"go.klarlabs.de/tokenops/internal/capability/headroom"
	"go.klarlabs.de/tokenops/internal/capability/sessions"
	"go.klarlabs.de/tokenops/internal/capability/spending"
	"go.klarlabs.de/tokenops/internal/contexts/governance/agentdx"
	"go.klarlabs.de/tokenops/internal/contexts/observability/analytics"
	"go.klarlabs.de/tokenops/internal/otlp"
)

// Inputs are the readings the gauges come from; any may be missing.
type Inputs struct {
	Glance   *headroom.Glance
	Findings *findings.Report
	// Costs is each provider's usage today and over 30 days.
	Costs   map[string]spending.ProviderCost
	DX      *sessions.DX
	Commits *commits.Report
}

// Gauges are the figures in, named in the tokenops.* namespace.
func Gauges(in Inputs) []otlp.Gauge {
	var out []otlp.Gauge
	add := func(g otlp.Gauge) {
		if len(g.Points) > 0 {
			out = append(out, g)
		}
	}
	if in.Glance != nil {
		used := otlp.Gauge{Name: "tokenops.plan.window.utilization", Unit: "%", Description: "Share of a plan window used, as the vendor reports it"}
		pace := otlp.Gauge{Name: "tokenops.plan.window.pace", Unit: "%", Description: "Share used minus share of the window gone by; positive runs ahead"}
		runsOut := otlp.Gauge{Name: "tokenops.plan.window.runs_out_in", Unit: "s", Description: "When a window ahead of pace runs out at the rate so far"}
		spendUsed := otlp.Gauge{Name: "tokenops.plan.spend.utilization", Unit: "%", Description: "Spend against a plan's limit this period"}
		for _, r := range in.Glance.Headroom.Reports {
			for _, w := range r.Windows {
				attrs := map[string]string{"provider": r.Provider, "plan": r.PlanName, "window": w.Name}
				used.Points = append(used.Points, otlp.Point{Attrs: attrs, Value: w.UsedPct})
				if w.Pace != nil {
					pace.Points = append(pace.Points, otlp.Point{Attrs: attrs, Value: w.Pace.DeltaPct})
					if !w.Pace.LastsToReset && w.Pace.RunsOutIn > 0 {
						runsOut.Points = append(runsOut.Points, otlp.Point{Attrs: attrs, Value: w.Pace.RunsOutIn.Seconds()})
					}
				}
			}
			if r.SpendLimitUSD > 0 {
				spendUsed.Points = append(spendUsed.Points, otlp.Point{Attrs: map[string]string{"provider": r.Provider, "plan": r.PlanName}, Value: r.SpendPct})
			}
		}
		add(used)
		add(pace)
		add(runsOut)
		add(spendUsed)
	}
	if len(in.Costs) > 0 {
		tokens := otlp.Gauge{Name: "tokenops.usage.tokens", Unit: "{token}", Description: "Tokens used over a trailing window"}
		value := otlp.Gauge{Name: "tokenops.usage.api_equivalent", Unit: "USD", Description: "Usage at API list prices over a trailing window; its value where a plan covers it"}
		billed := otlp.Gauge{Name: "tokenops.usage.cost", Unit: "USD", Description: "What was billed over a trailing window"}
		unpriced := otlp.Gauge{Name: "tokenops.usage.unpriced_share", Unit: "%", Description: "Share of requests on models with no list price, left out of the money"}
		for provider, c := range in.Costs {
			for _, w := range []struct {
				name string
				u    spending.Usage
			}{{"24h", c.Today}, {"30d", c.Last30}} {
				attrs := map[string]string{"provider": provider, "period": w.name}
				tokens.Points = append(tokens.Points, otlp.Point{Attrs: attrs, Value: float64(w.u.Tokens)})
				value.Points = append(value.Points, otlp.Point{Attrs: attrs, Value: w.u.APIEquivalentUSD})
				billed.Points = append(billed.Points, otlp.Point{Attrs: attrs, Value: w.u.CostUSD})
				unpriced.Points = append(unpriced.Points, otlp.Point{Attrs: attrs, Value: w.u.UnpricedShare() * 100})
			}
		}
		add(tokens)
		add(value)
		add(billed)
		add(unpriced)
	}
	if in.DX != nil && in.DX.Metrics.Prompts > 0 {
		m := in.DX.Metrics
		dx := otlp.Gauge{Name: "tokenops.sessions.dx", Description: "How sessions go over the last week; see tokenops explain"}
		for name, v := range map[string]float64{
			"median_turns_per_instruction": m.MedianTurnsPerPrompt,
			"first_try_rate_pct":           m.FirstTryRatePct,
			"rework_rate_pct":              m.ReworkRatePct,
			"interrupt_rate_pct":           m.InterruptRatePct,
			"escalation_rate_pct":          m.EscalationRatePct,
			"compactions_per_session":      m.CompactionsPerSession,
		} {
			dx.Points = append(dx.Points, otlp.Point{Attrs: map[string]string{"measure": name}, Value: v})
		}
		add(dx)
		grade := otlp.Gauge{Name: "tokenops.sessions.grade", Unit: "1", Description: "Grade per dimension: A=4, B=3, C=2, F=0"}
		g := in.DX.Grades
		for name, l := range map[string]agentdx.Letter{
			"overall": g.Overall, "turns": g.Turns, "duration": g.Duration, "rework": g.Rework,
			"interrupt": g.Interrupt, "escalation": g.Escalation, "first_try": g.FirstTry,
			"context_growth": g.ContextGrowth, "compaction": g.Compaction,
		} {
			if v, ok := letterValue(l); ok {
				grade.Points = append(grade.Points, otlp.Point{Attrs: map[string]string{"dimension": name}, Value: v})
			}
		}
		add(grade)
	}
	if in.Findings != nil {
		counts := map[[2]string]int{}
		for _, f := range in.Findings.Findings {
			counts[[2]string{f.Level, f.Kind}]++
		}
		found := otlp.Gauge{Name: "tokenops.coach.findings", Unit: "{finding}", Description: "The coach's current findings by level and kind"}
		for k, n := range counts {
			found.Points = append(found.Points, otlp.Point{Attrs: map[string]string{"level": k[0], "kind": k[1]}, Value: float64(n)})
		}
		add(found)
	}
	if in.Commits != nil && in.Commits.CommitsTotal > 0 {
		c := in.Commits
		add(otlp.Gauge{Name: "tokenops.commits.cost", Unit: "USD", Description: "Agent work per commit over the last week, at API prices",
			Points: []otlp.Point{
				{Attrs: map[string]string{"statistic": "median"}, Value: c.MedianUSD},
				{Attrs: map[string]string{"statistic": "mean"}, Value: c.MeanUSD},
			}})
		add(otlp.Gauge{Name: "tokenops.commits.count", Unit: "{commit}", Description: "Commits over the last week that followed agent work",
			Points: []otlp.Point{{Value: float64(c.CommitsTotal)}}})
		add(otlp.Gauge{Name: "tokenops.commits.attributed_share", Unit: "%", Description: "Share of the week's work placed on a commit",
			Points: []otlp.Point{{Value: c.AttributedShare * 100}}})
	}
	return out
}

// letterValue maps a grade to a number a dashboard can chart.
func letterValue(l agentdx.Letter) (float64, bool) {
	v, ok := map[agentdx.Letter]float64{agentdx.LetterA: 4, agentdx.LetterB: 3, agentdx.LetterC: 2, agentdx.LetterF: 0}[l]
	return v, ok
}

// Every is how often the daemon pushes; Commits is computed less often,
// since it runs git across every repository.
const (
	Every        = time.Minute
	CommitsEvery = 30 * time.Minute
)

// Gatherer reads the figures for one push. The daemon keeps one and calls
// Gather every interval; `tokenops otel` calls it once to show what would
// leave the machine. Cost per commit, which runs git across every
// repository, is read at most every CommitsEvery.
type Gatherer struct {
	Glance func() headroom.Deps
	Agg    *analytics.Aggregator
	// Coach is the coach's report, for its findings; nil leaves them out.
	Coach func(now time.Time) *coachcap.Report

	commits     *commits.Report
	commitsRead time.Time
}

// Gather reads every figure the gauges are made of.
func (g *Gatherer) Gather(ctx context.Context, now time.Time) Inputs {
	var in Inputs
	if glance, err := headroom.ComputeGlance(ctx, g.Glance(), now); err == nil {
		in.Glance = &glance
		var report *coachcap.Report
		if g.Coach != nil {
			report = g.Coach(now)
		}
		f := findings.Compute(findings.Gather(&glance, report, findings.DefaultDir()))
		in.Findings = &f
		in.Costs = map[string]spending.ProviderCost{}
		for _, r := range glance.Headroom.Reports {
			if _, done := in.Costs[r.Provider]; done || r.Provider == "" {
				continue
			}
			if c, err := spending.CostOf(ctx, g.Agg, r.Provider, now); err == nil {
				in.Costs[r.Provider] = c
			}
		}
	}
	if s, err := findings.ReadSnapshot(findings.DefaultDir()); err == nil && s != nil && now.Sub(s.ComputedAt) <= findings.MaxSnapshotAge {
		in.DX = &s.DX
	}
	if g.commits == nil || now.Sub(g.commitsRead) >= CommitsEvery {
		if r, err := commits.Compute(ctx, commits.Deps{
			Turns: func(ctx context.Context, since time.Time) ([]analytics.SessionTurn, error) {
				return g.Agg.SessionTurns(ctx, analytics.Filter{Since: since})
			},
		}, now.Add(-7*24*time.Hour)); err == nil {
			g.commits, g.commitsRead = &r, now
		}
	}
	in.Commits = g.commits
	return in
}
