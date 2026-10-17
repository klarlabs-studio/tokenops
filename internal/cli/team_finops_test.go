package cli

import (
	"bytes"
	"strings"
	"testing"

	"go.klarlabs.de/tokenops/pkg/teamwire"
)

func TestWriteFinOps(t *testing.T) {
	util := 64.0
	u := teamwire.Upload{
		PlanDays: []teamwire.PlanDay{{Day: "2026-10-08", Provider: "anthropic", Plan: "claude-max-5x", UtilizationPct: &util, Lockouts: 1, BlockedMinutes: 40}},
		Waste:    []teamwire.Waste{{Day: "2026-10-08", Repo: "acme/api", Kind: "edit", Class: teamwire.WasteReread, Turns: 2, Tokens: 1500, APIEquivalentUSD: 0.75}},
		Savings:  []teamwire.SavingsDay{{Day: "2026-10-08", Interventions: 2, RealizedUSD: 0.5, AvoidableUSD: 0.75}},
		PlanAdvice: []teamwire.PlanAdvice{{Provider: "anthropic", Plan: "claude-max-20x", Verdict: "switch", Kind: "plan",
			RecommendedPlan: "claude-max-5x", MonthlyDeltaUSD: 100, Confidence: "medium", Days: 30}},
	}
	cases := map[string]struct {
		joined bool
		schema int
		want   string
	}{
		"not joined":      {false, 0, "Sent only to a team server that accepts upload schema 2"},
		"schema 1 server": {true, 1, "Withheld: the team server reads upload schema 1 only"},
		"schema 2 server": {true, 2, "Sent with the buckets (upload schema 2)"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			var out bytes.Buffer
			writeFinOps(&out, u, finOpsNote(c.joined, c.schema))
			got := out.String()
			for _, want := range []string{c.want, "Plan use: 1 rows", "64%", "redundant_reread", "Realized savings: 1 days",
				"$0.50", "Plan advice: 1 providers", "claude-max-5x", "$100.00"} {
				if !strings.Contains(got, want) {
					t.Errorf("preview lacks %q:\n%s", want, got)
				}
			}
		})
	}
	var out bytes.Buffer
	writeFinOps(&out, teamwire.Upload{}, finOpsNote(true, 2))
	if !strings.Contains(out.String(), "No plan use, waste, savings or plan advice") {
		t.Errorf("empty preview = %q", out.String())
	}
}
