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
		"schema 4 member": {4, teamwire.RoleMember, "accepts upload schema 4", "schema 3"},
		"schema 4 owner":  {4, teamwire.RoleOwner, "vendor's daily bill", "schema 3"},
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

// `team status` says how long the server keeps the member's own rows when
// that is shorter than the team totals, and only the totals' retention
// for a server that does not say.
func TestWriteMeNamesPersonalRetention(t *testing.T) {
	cases := map[string]struct {
		me     teamwire.Me
		want   string
		absent string
	}{
		"personal":     {teamwire.Me{RetentionDays: 400, PersonalRetentionDays: 90}, "kept 90 days after their week is released; team totals are kept 400 days", "), kept 400 days"},
		"older server": {teamwire.Me{RetentionDays: 400}, "kept 400 days.", "after their week"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			var out bytes.Buffer
			writeMe(&out, c.me)
			got := out.String()
			if !strings.Contains(got, c.want) || strings.Contains(got, c.absent) {
				t.Errorf("status wants %q, not %q:\n%s", c.want, c.absent, got)
			}
		})
	}
}

// The seat value a preview shows: the plans bound, an idle one included,
// each day's value, every option the advice priced and where the week is
// headed, pinned by a golden file.
func TestWriteSeatsGolden(t *testing.T) {
	value, unpriced, list, pro, maxPlan, api, peak := 12.5, 0.0, 250.0, 20.0, 100.0, 271.79, 75.0
	u := teamwire.Upload{
		PlanDays: []teamwire.PlanDay{
			{Day: "2026-11-12", Provider: "anthropic", Plan: "claude-max", PlanValueUSD: &value, PlanUnpricedTurns: 2},
			{Day: "2026-11-13", Provider: "anthropic", Plan: "claude-max", PlanValueUSD: &unpriced},
			// Not priced: no rate card, so not shown.
			{Day: "2026-11-13", Provider: "zai", Plan: "pay-as-you-go"},
		},
		PlanAdvice: []teamwire.PlanAdvice{{Provider: "anthropic", Plan: "claude-max", Verdict: "switch", Kind: "plan",
			RecommendedPlan: "claude-pro", MonthlyDeltaUSD: 80, Confidence: "medium", Days: 28, ListValueUSD: &list,
			Options: []teamwire.AdviceOption{
				{Kind: "plan", Plan: "claude-pro", MonthlyUSD: &pro, PeakPct: &peak},
				{Kind: "plan", Plan: "claude-max", MonthlyUSD: &maxPlan},
				{Kind: "api", MonthlyUSD: &api, Lockouts: 0},
			}}},
		PlanBindings: []teamwire.PlanBinding{
			{Provider: "anthropic", Plan: "claude-max", Since: "2026-09-01"},
			{Provider: "openai", Plan: "gpt-plus", Shared: true},
		},
		PlanWindows: []teamwire.PlanWindow{{Provider: "anthropic", Window: teamwire.WindowWeekly, UsedPct: 61,
			ProjectedPctAtReset: 112, RunsOut: true, ShortHours: 12, ResetsOn: "2026-11-16", Basis: teamwire.BasisAverage}},
	}
	var out bytes.Buffer
	writeSeats(&out, u, seatsNote(true, teamwire.SchemaSeats))
	assertGolden(t, "team/preview-seats.txt", out.String())

	for _, c := range []struct {
		joined bool
		schema int
		want   string
	}{
		{false, 0, "Sent only to a team server that accepts upload schema 6"},
		{true, teamwire.SchemaPrevented, "Withheld: the team server does not accept upload schema 6 yet"},
		{true, teamwire.SchemaSeats, "Sent with the buckets (upload schema 6)"},
	} {
		if got := seatsNote(c.joined, c.schema); !strings.Contains(got, c.want) {
			t.Errorf("joined %v, schema %d: note %q, want %q", c.joined, c.schema, got, c.want)
		}
	}
	out.Reset()
	writeSeats(&out, teamwire.Upload{}, seatsNote(true, teamwire.SchemaSeats))
	if !strings.Contains(out.String(), "No plans bound") {
		t.Errorf("empty preview = %q", out.String())
	}
}

// The spend by model a preview shows, pinned by a golden file; an upload
// without it shows nothing.
func TestWriteModelSpendGolden(t *testing.T) {
	u := teamwire.Upload{SpendModels: []teamwire.ModelSpend{
		{Day: "2026-11-12", Repo: "acme/api", Provider: "anthropic", Model: "claude-opus-4-8", Agent: "claude-code",
			Effort: "high", Turns: 3, Tokens: 1000, CostUSD: 0.1, APIEquivalentUSD: 1.0},
		{Day: "2026-11-12", Repo: "acme/api", Provider: "anthropic", Model: teamwire.ModelOther, Agent: "opencode",
			Effort: teamwire.EffortUnknown, Turns: 1, Tokens: 200, UnpricedTurns: 1},
	}}
	var out bytes.Buffer
	writeModelSpend(&out, u, seatsNote(true, teamwire.SchemaSeats))
	assertGolden(t, "team/preview-models.txt", out.String())
	out.Reset()
	writeModelSpend(&out, teamwire.Upload{}, seatsNote(true, teamwire.SchemaSeats))
	if out.Len() != 0 {
		t.Errorf("empty preview = %q", out.String())
	}
}
