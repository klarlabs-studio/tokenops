package findings

import (
	"strings"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/anomalies"
	"go.klarlabs.de/tokenops/internal/contexts/spend/anomaly"
)

func pct(v float64) *float64 { return &v }

// An unusual day shows on glance as one ambient finding: the figures,
// the likely cause by name, the ranked contributors, and how to answer
// it like a tip.
func TestAnomalyFindings(t *testing.T) {
	day := time.Date(2026, 10, 14, 0, 0, 0, 0, time.UTC)
	cases := []struct {
		name string
		a    anomalies.Anomaly
		want []string
	}{
		{
			name: "billed per token under a plan",
			a: anomalies.Anomaly{Anomaly: anomaly.Anomaly{ID: "anomaly-spend-2026-10-14", Metric: anomaly.MetricSpend, Day: day,
				ActualUSD: 24, ExpectedUSD: 0, ExcessUSD: 24,
				Contributors: []anomaly.Contributor{{Dimension: anomaly.DimBiller, Value: "anthropic via anthropic-api", DeltaUSD: 24, Share: 1}},
				Cause: anomaly.Cause{Kind: anomaly.CauseMeteredOnPlan, Value: "anthropic", Endpoint: "anthropic-api",
					CoveragePct: pct(20), BaselineCoveragePct: pct(99)}}},
			want: []string{
				"Spend on Wed 14 Oct was $24.00, $24.00 over the usual $0.00",
				"Claude usage was billed per token while its plan should cover it, through anthropic-api",
				"plan coverage 20% against 99% before",
				"biller anthropic via anthropic-api +$24.00 (100%)",
				"tokenops coach tip expected --id anomaly-spend-2026-10-14",
			},
		},
		{
			name: "pricier model",
			a: anomalies.Anomaly{Anomaly: anomaly.Anomaly{ID: "anomaly-usage-2026-10-14", Metric: anomaly.MetricUsage, Day: day, Partial: true,
				ActualUSD: 50, ExpectedUSD: 10, ExcessUSD: 40,
				Cause: anomaly.Cause{Kind: anomaly.CausePricierModel, Value: "opus", USDPerMTok: 50, BaselineUSDPerMTok: 10}}},
			want: []string{
				"Usage today is already $50.00 at API prices, $40.00 over the usual $10.00",
				"opus took over at $50.00 per million tokens against $10.00 before",
				"Check which model your client starts sessions on",
			},
		},
		{
			name: "runaway loop",
			a: anomalies.Anomaly{Anomaly: anomaly.Anomaly{ID: "anomaly-usage-2026-10-14", Metric: anomaly.MetricUsage, Day: day,
				ActualUSD: 70, ExpectedUSD: 10, ExcessUSD: 60,
				Cause: anomaly.Cause{Kind: anomaly.CauseRunawayLoop, Value: "loop", Repo: "warden", Share: 0.9}},
				Workflow: "claude-code:p:loop", Waste: []string{"Repeated agent loop detected"}},
			want: []string{
				"one session in warden carried 90% of the rise, as a runaway loop does",
				"the waste detector found: Repeated agent loop detected",
				"`tokenops replay --workflow claude-code:p:loop`",
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fs := anomalyFindings([]anomalies.Anomaly{tc.a})
			if len(fs) != 1 {
				t.Fatalf("findings = %+v", fs)
			}
			f := fs[0]
			if f.Kind != KindSpend || f.Level != LevelNotice || f.ID != tc.a.ID {
				t.Errorf("kind %q level %q id %q", f.Kind, f.Level, f.ID)
			}
			all := f.Title + " | " + f.Evidence + " | " + f.Action
			for _, w := range tc.want {
				if !strings.Contains(all, w) {
					t.Errorf("missing %q in %q", w, all)
				}
			}
		})
	}
}

// The findings carry what the anomaly snapshot still holds open.
func TestComputeIncludesOpenAnomalies(t *testing.T) {
	a := anomalies.Anomaly{Anomaly: anomaly.Anomaly{ID: "x", Metric: anomaly.MetricUsage, Cause: anomaly.Cause{Kind: anomaly.CauseSpike}}}
	r := Compute(Inputs{Anomalies: []anomalies.Anomaly{a}})
	if len(r.Findings) != 1 || r.Findings[0].Kind != KindSpend {
		t.Fatalf("findings = %+v", r.Findings)
	}
}
