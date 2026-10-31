package cli

import (
	"bytes"
	"strings"
	"testing"

	"go.klarlabs.de/tokenops/pkg/teamwire"
)

// `team status` names the newest schema the server accepts, and tells an
// owner or admin that their machine may send the vendor's bill.
func TestWriteMeNamesTheServersSchema(t *testing.T) {
	cases := map[string]struct {
		schema int
		role   string
		want   string
		absent string
	}{
		"schema 1":        {1, teamwire.RoleMember, "reads upload schema 1 only", "schema 2"},
		"schema 2":        {2, teamwire.RoleMember, "accepts upload schema 2", "schema 3"},
		"schema 3 member": {3, teamwire.RoleMember, "accepts upload schema 3", "vendor's daily bill"},
		"schema 3 owner":  {3, teamwire.RoleOwner, "vendor's daily bill", "schema 2"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			var out bytes.Buffer
			writeMe(&out, teamwire.Me{MaxSchema: c.schema, Role: c.role})
			got := out.String()
			if !strings.Contains(got, c.want) || strings.Contains(got, c.absent) {
				t.Errorf("status wants %q, not %q:\n%s", c.want, c.absent, got)
			}
		})
	}
}

func TestWriteFinOps(t *testing.T) {
	util := 64.0
	u := teamwire.Upload{
		PlanDays: []teamwire.PlanDay{{Day: "2026-10-08", Provider: "anthropic", Plan: "claude-max", UtilizationPct: &util, Lockouts: 1, BlockedMinutes: 40}},
		Waste:    []teamwire.Waste{{Day: "2026-10-08", Repo: "acme/api", Kind: "edit", Class: teamwire.WasteReread, Turns: 2, Tokens: 1500, APIEquivalentUSD: 0.75}},
		Savings:  []teamwire.SavingsDay{{Day: "2026-10-08", Interventions: 2, RealizedUSD: 0.5, AvoidableUSD: 0.75}},
		PlanAdvice: []teamwire.PlanAdvice{{Provider: "anthropic", Plan: "claude-ultra", Verdict: "switch", Kind: "plan",
			RecommendedPlan: "claude-max", MonthlyDeltaUSD: 100, Confidence: "medium", Days: 30}},
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
				"$0.50", "Plan advice: 1 providers", "claude-max", "$100.00"} {
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
