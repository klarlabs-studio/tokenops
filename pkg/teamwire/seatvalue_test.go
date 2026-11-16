package teamwire

import (
	"encoding/json"
	"strings"
	"testing"
)

// seatUpload is a valid schema 6 upload with every seat-value addition:
// a day's value, the advice's list value and options, a binding and the
// forward snapshot. It is computed on Friday 2026-11-13, and the week
// resets on Monday 2026-11-16. The figures are invented.
func seatUpload() Upload {
	u := preventedUpload()
	u.Schema = SchemaSeats
	u.Days = []string{"2026-11-12", "2026-11-13"}
	u.Buckets = nil
	u.PlanDays = []PlanDay{{Day: "2026-11-12", Provider: "anthropic", Plan: "claude-max", PlanTokens: 800,
		CoveragePct: pct(100), PlanValueUSD: pct(12.5), PlanUnpricedTurns: 2}}
	u.PlanAdvice = []PlanAdvice{{Provider: "anthropic", Plan: "claude-max", Verdict: "switch", Kind: "plan",
		RecommendedPlan: "claude-pro", MonthlyDeltaUSD: 80, Material: true, Confidence: "medium", Days: 28,
		WindowsMeasured: 4, ListValueUSD: pct(250),
		Options: []AdviceOption{
			{Kind: "plan", Plan: "claude-pro", MonthlyUSD: pct(20), PeakPct: pct(75)},
			{Kind: "plan", Plan: "claude-max", MonthlyUSD: pct(100), PeakPct: pct(15)},
			{Kind: "plan", Plan: "claude-ultra", MonthlyUSD: pct(200), PeakPct: pct(7.5)},
			{Kind: "plan_overflow", Plan: "claude-pro", MonthlyUSD: pct(41.2), Lockouts: 2, BlockedMinutes: 600, PeakPct: pct(120)},
			{Kind: "api", MonthlyUSD: pct(271.79)},
		}}}
	u.PlanBindings = []PlanBinding{
		{Provider: "anthropic", Plan: "claude-max", Since: "2026-09-01"},
		{Provider: "openai", Plan: "gpt-plus", Shared: true},
	}
	u.PlanWindows = []PlanWindow{{Provider: "anthropic", Window: WindowWeekly, UsedPct: 61, ProjectedPctAtReset: 112,
		RunsOut: true, ShortHours: 12, ResetsOn: "2026-11-16", Basis: BasisAverage}}
	return u
}

func TestSeatValueValidate(t *testing.T) {
	neg := -1.0
	tests := []struct {
		name   string
		change func(*Upload)
		want   string
	}{
		{"valid", func(*Upload) {}, ""},
		{"a binding alone, an idle seat", func(u *Upload) {
			u.PlanDays, u.PlanAdvice, u.PlanWindows = nil, nil, nil
		}, ""},
		// Schema 5 carries none of the four.
		{"schema 5 with a day's value", func(u *Upload) {
			u.Schema = SchemaPrevented
			u.PlanBindings, u.PlanWindows, u.PlanAdvice = nil, nil, nil
		}, "carries no seat value"},
		{"schema 5 with unpriced turns", func(u *Upload) {
			u.Schema = SchemaPrevented
			u.PlanBindings, u.PlanWindows, u.PlanAdvice = nil, nil, nil
			u.PlanDays[0].PlanValueUSD = nil
		}, "carries no seat value"},
		{"schema 5 with a list value", func(u *Upload) {
			u.Schema = SchemaPrevented
			u.PlanBindings, u.PlanWindows, u.PlanDays = nil, nil, nil
			u.PlanAdvice[0].Options = nil
		}, "carries no seat value"},
		{"schema 5 with options", func(u *Upload) {
			u.Schema = SchemaPrevented
			u.PlanBindings, u.PlanWindows, u.PlanDays = nil, nil, nil
			u.PlanAdvice[0].ListValueUSD = nil
		}, "carries no seat value"},
		{"schema 5 with a binding", func(u *Upload) {
			u.Schema = SchemaPrevented
			u.PlanWindows, u.PlanDays, u.PlanAdvice = nil, nil, nil
		}, "carries no seat value"},
		{"schema 5 with a window", func(u *Upload) {
			u.Schema = SchemaPrevented
			u.PlanBindings, u.PlanDays, u.PlanAdvice = nil, nil, nil
		}, "carries no seat value"},
		// plan_days.
		{"a negative day's value", func(u *Upload) { u.PlanDays[0].PlanValueUSD = &neg }, "plan_value_usd"},
		{"a day's value past the bound", func(u *Upload) { u.PlanDays[0].PlanValueUSD = pct(1e7 + 1) }, "plan_value_usd"},
		{"negative unpriced turns", func(u *Upload) { u.PlanDays[0].PlanUnpricedTurns = -1 }, "negative"},
		{"unpriced turns with no value", func(u *Upload) { u.PlanDays[0].PlanValueUSD = nil }, "plan_unpriced_turns"},
		// plan_advice.
		{"a negative list value", func(u *Upload) { u.PlanAdvice[0].ListValueUSD = &neg }, "list_value_usd"},
		{"an unknown option kind", func(u *Upload) { u.PlanAdvice[0].Options[0].Kind = "a cheaper plan" }, "not an option kind"},
		{"an option plan off the catalog", func(u *Upload) { u.PlanAdvice[0].Options[0].Plan = "my team's plan" }, "not a catalog plan"},
		{"an option plan of another provider", func(u *Upload) { u.PlanAdvice[0].Options[0].Plan = "gpt-plus" }, "not a catalog plan"},
		{"an API option naming a plan", func(u *Upload) { u.PlanAdvice[0].Options[4].Plan = "claude-pro" }, "names no plan"},
		{"an option twice", func(u *Upload) { u.PlanAdvice[0].Options[1] = u.PlanAdvice[0].Options[0] }, "appears twice"},
		{"a negative monthly cost", func(u *Upload) { u.PlanAdvice[0].Options[0].MonthlyUSD = &neg }, "monthly_usd"},
		{"negative option lockouts", func(u *Upload) { u.PlanAdvice[0].Options[0].Lockouts = -1 }, "negative"},
		{"option blocked past the period", func(u *Upload) {
			u.PlanAdvice[0].Options[0].BlockedMinutes = MaxAdviceDays*24*60 + 1
		}, "blocked_minutes"},
		{"a negative peak", func(u *Upload) { u.PlanAdvice[0].Options[0].PeakPct = &neg }, "peak_pct"},
		{"a peak past the bound", func(u *Upload) { u.PlanAdvice[0].Options[0].PeakPct = pct(MaxPeakPct + 1) }, "peak_pct"},
		{"too many options", func(u *Upload) {
			for len(u.PlanAdvice[0].Options) <= MaxAdviceOptions {
				u.PlanAdvice[0].Options = append(u.PlanAdvice[0].Options, u.PlanAdvice[0].Options[0])
			}
		}, "at most"},
		// plan_bindings.
		{"a binding off the catalog", func(u *Upload) { u.PlanBindings[0].Plan = "Acme seat" }, "not a catalog plan"},
		{"a binding twice", func(u *Upload) { u.PlanBindings[1] = u.PlanBindings[0] }, "appears twice"},
		{"a binding since a timestamp", func(u *Upload) { u.PlanBindings[0].Since = "2026-09-01T08:15:00Z" }, "not a date"},
		{"a binding since after the upload", func(u *Upload) { u.PlanBindings[0].Since = "2026-11-14" }, "after computed_at"},
		// plan_windows.
		{"a 5-hour window", func(u *Upload) { u.PlanWindows[0].Window = "5h" }, "not a window"},
		{"a monthly window", func(u *Upload) { u.PlanWindows[0].Window = WindowMonthly }, ""},
		{"used past 100", func(u *Upload) { u.PlanWindows[0].UsedPct = 101 }, "used_pct"},
		{"negative used", func(u *Upload) { u.PlanWindows[0].UsedPct = -1 }, "used_pct"},
		{"a projection past 999", func(u *Upload) { u.PlanWindows[0].ProjectedPctAtReset = 1000 }, "projected_pct_at_reset"},
		{"a negative projection", func(u *Upload) { u.PlanWindows[0].ProjectedPctAtReset = -1 }, "projected_pct_at_reset"},
		{"short hours past a week", func(u *Upload) { u.PlanWindows[0].ShortHours = 174 }, "short_hours"},
		{"short hours not in 6-hour steps", func(u *Upload) { u.PlanWindows[0].ShortHours = 13 }, "short_hours"},
		{"short hours on a window that lasts", func(u *Upload) {
			u.PlanWindows[0].RunsOut, u.PlanWindows[0].ProjectedPctAtReset = false, 90
		}, "short_hours"},
		{"runs out projected to last", func(u *Upload) { u.PlanWindows[0].ProjectedPctAtReset = 90 }, "runs_out"},
		{"a reset as a timestamp", func(u *Upload) { u.PlanWindows[0].ResetsOn = "2026-11-16T09:00:00Z" }, "not a date"},
		{"a reset before the upload", func(u *Upload) { u.PlanWindows[0].ResetsOn = "2026-11-12" }, "resets_on"},
		{"a weekly reset past a week out", func(u *Upload) { u.PlanWindows[0].ResetsOn = "2026-11-21" }, "resets_on"},
		{"an unknown basis", func(u *Upload) { u.PlanWindows[0].Basis = "my guess" }, "not a basis"},
		{"too early with a projection", func(u *Upload) { u.PlanWindows[0].TooEarly = true }, "too_early"},
		{"too early, nothing projected", func(u *Upload) {
			w := &u.PlanWindows[0]
			w.TooEarly, w.RunsOut, w.ShortHours, w.ProjectedPctAtReset = true, false, 0, 0
		}, ""},
		{"a window twice", func(u *Upload) { u.PlanWindows = append(u.PlanWindows, u.PlanWindows[0]) }, "appears twice"},
		{"a window of an unknown provider", func(u *Upload) { u.PlanWindows[0].Provider = "acme" }, "provider"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			u := seatUpload()
			tt.change(&u)
			err := u.Validate()
			if tt.want == "" {
				if err != nil {
					t.Fatalf("valid upload refused: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("err = %v; want %q", err, tt.want)
			}
		})
	}
}

// The forward snapshot is a date and whole figures: no reset instant, no
// run-out time.
func TestPlanWindowJSONNames(t *testing.T) {
	b, err := jsonOf(seatUpload().PlanWindows[0])
	if err != nil {
		t.Fatal(err)
	}
	want := `{"provider":"anthropic","window":"weekly","used_pct":61,"projected_pct_at_reset":112,"runs_out":true,` +
		`"short_hours":12,"resets_on":"2026-11-16","basis":"average","too_early":false}`
	if b != want {
		t.Errorf("got  %s\nwant %s", b, want)
	}
}

// A server below schema 6 gets schema 5 with none of the four additions,
// and no key it does not know.
func TestForServerDropsSeatValueBelowSchema6(t *testing.T) {
	full := seatUpload()
	u := full.ForServer(SchemaPrevented)
	if u.Schema != SchemaPrevented || u.PlanBindings != nil || u.PlanWindows != nil {
		t.Fatalf("for a schema 5 server: %+v", u)
	}
	if u.PlanDays[0].PlanValueUSD != nil || u.PlanDays[0].PlanUnpricedTurns != 0 ||
		u.PlanAdvice[0].ListValueUSD != nil || u.PlanAdvice[0].Options != nil {
		t.Fatalf("for a schema 5 server the rows keep seat value: %+v %+v", u.PlanDays, u.PlanAdvice)
	}
	if len(u.PlanDays) != 1 || len(u.PlanAdvice) != 1 || len(u.Prevented) == 0 {
		t.Fatalf("for a schema 5 server the schema 5 sections are lost: %+v", u)
	}
	b, _ := json.Marshal(u)
	for _, key := range []string{"plan_value_usd", "plan_unpriced_turns", "list_value_usd", "options", "plan_bindings", "plan_windows"} {
		if strings.Contains(string(b), `"`+key+`"`) {
			t.Errorf("a schema 5 upload carries %s: %s", key, b)
		}
	}
	if err := u.Validate(); err != nil {
		t.Errorf("the schema 5 upload is invalid: %v", err)
	}
	// The rows are copied, not changed under the caller.
	if full.PlanDays[0].PlanValueUSD == nil || full.PlanAdvice[0].Options == nil {
		t.Error("ForServer changed the upload it was given")
	}
	for _, maxSchema := range []int{SchemaBuckets, SchemaFinOps, SchemaSpend, SchemaReverts} {
		if err := full.ForServer(maxSchema).Validate(); err != nil {
			t.Errorf("ForServer(%d) invalid: %v", maxSchema, err)
		}
	}
	if u := full.ForServer(SchemaSeats + 1); u.Schema != SchemaSeats || u.PlanWindows == nil {
		t.Errorf("a newer server gets schema 6 whole: %+v", u)
	}
}
