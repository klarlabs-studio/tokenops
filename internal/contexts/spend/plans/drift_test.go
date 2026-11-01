package plans

import (
	"math"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// driftWeek is one synthetic weekly window: how much list value one
// percent of it held, and the models that spent it.
type driftWeek struct {
	valuePerPct float64
	models      []string
}

// driftHistory lays weeks end to end from start, the first `lower` on
// lowPlan and the rest on highPlan, each filled to 40% with the value its
// ratio gives, spread evenly over its models.
func driftHistory(start time.Time, lowPlan, highPlan string, lower []driftWeek, higher []driftWeek) DriftInput {
	var in DriftInput
	in.Provider, in.Since = "anthropic", start
	switchAt := start.Add(time.Duration(len(lower)) * week)
	in.PlanAt = func(t time.Time) string {
		if t.Before(switchAt) {
			return lowPlan
		}
		return highPlan
	}
	for i, w := range append(append([]driftWeek(nil), lower...), higher...) {
		open := start.Add(time.Duration(i) * week)
		reset := open.Add(week)
		peakAt := open.Add(3 * 24 * time.Hour)
		in.Readings = append(in.Readings,
			QuotaReading{At: open.Add(time.Hour), Windows: []QuotaWindow{{Label: "weekly", UsedPct: 2, ResetsAt: reset, Duration: week}}},
			QuotaReading{At: peakAt, Windows: []QuotaWindow{{Label: "weekly", UsedPct: 40, ResetsAt: reset.Add(time.Minute), Duration: week}}},
		)
		value := w.valuePerPct * 40
		for j, m := range w.models {
			in.Turns = append(in.Turns, DriftTurn{At: open.Add(time.Duration(j+1) * time.Hour), Model: m, ValueUSD: value / float64(len(w.models))})
		}
		// Use after the fullest reading is not counted against it.
		in.Turns = append(in.Turns, DriftTurn{At: peakAt.Add(time.Hour), Model: w.models[0], ValueUSD: 1000})
	}
	return in
}

func weeks(n int, models []string, vpp ...float64) []driftWeek {
	out := make([]driftWeek, n)
	for i := range out {
		out[i] = driftWeek{valuePerPct: vpp[i%len(vpp)], models: models}
	}
	return out
}

// The operator's own windows on two tiers imply a ratio between them; the
// catalog states one. A gap past the threshold and past the windows' own
// spread is drift. Too few windows, windows that mixed model families, or
// a single tier say nothing.
func TestTierDrifts(t *testing.T) {
	start := time.Date(2026, 8, 3, 0, 0, 0, 0, time.UTC)
	sonnet := []string{"claude-sonnet-5"}
	mixed := []string{"claude-sonnet-5", "claude-opus-5-5"}
	cases := []struct {
		name          string
		low, high     string
		lower, higher []driftWeek
		// want is the comparison expected, nil for none.
		want *struct {
			catalog, implied float64
			drifted          bool
		}
	}{
		{
			name: "agrees with the catalog", low: "claude-pro", high: "claude-max",
			lower: weeks(5, sonnet, 1.0, 1.1, 0.9), higher: weeks(6, sonnet, 5.0, 5.4, 4.6),
			want: &struct {
				catalog, implied float64
				drifted          bool
			}{5, 5, false},
		},
		{
			name: "disagrees beyond the threshold", low: "claude-pro", high: "claude-max",
			lower: weeks(5, sonnet, 1.0, 1.05, 0.95), higher: weeks(5, sonnet, 3.0, 3.1, 2.9),
			want: &struct {
				catalog, implied float64
				drifted          bool
			}{5, 3, true},
		},
		{
			name: "a gap inside the windows' spread is noise", low: "claude-pro", high: "claude-max",
			lower: weeks(5, sonnet, 0.4, 0.6, 1, 1.4, 1.6), higher: weeks(5, sonnet, 2, 3, 3.5, 6, 8),
			want: &struct {
				catalog, implied float64
				drifted          bool
			}{5, 3.5, false},
		},
		{
			name: "a renamed tier is the same tier", low: "claude-pro", high: "claude-max-5x",
			lower: weeks(5, sonnet, 1.0), higher: weeks(5, sonnet, 2.0),
			want: &struct {
				catalog, implied float64
				drifted          bool
			}{5, 2, true},
		},
		{
			name: "weekly ratio to Ultra", low: "claude-pro", high: "claude-ultra",
			lower: weeks(5, sonnet, 1.0), higher: weeks(5, sonnet, 10.0),
			want: &struct {
				catalog, implied float64
				drifted          bool
			}{10, 10, false},
		},
		{
			name: "too few windows on one tier", low: "claude-pro", high: "claude-max",
			lower: weeks(5, sonnet, 1.0), higher: weeks(MinDriftWindows-1, sonnet, 3.0),
		},
		{
			name: "windows that mixed model families are left out", low: "claude-pro", high: "claude-max",
			lower: weeks(5, sonnet, 1.0), higher: weeks(6, mixed, 3.0),
		},
		{
			name: "one tier has nothing to compare with", low: "claude-max", high: "claude-max",
			lower: weeks(5, sonnet, 3.0), higher: weeks(5, sonnet, 3.0),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := TierDrifts(driftHistory(start, tc.low, tc.high, tc.lower, tc.higher))
			if tc.want == nil {
				if len(got) != 0 {
					t.Fatalf("want no comparison, got %+v", got)
				}
				return
			}
			if len(got) != 1 {
				t.Fatalf("want one comparison, got %+v", got)
			}
			d := got[0]
			if d.Window != "weekly" || d.ModelFamily != "sonnet" || d.Lower.Plan != "claude-pro" {
				t.Errorf("compared %+v", d)
			}
			if math.Abs(d.CatalogRatio-tc.want.catalog) > 1e-9 || math.Abs(d.ImpliedRatio-tc.want.implied) > 1e-9 || d.Drifted != tc.want.drifted {
				t.Errorf("catalog %.2f implied %.2f drifted %v (gap %.2f, range %.2f–%.2f), want %+v",
					d.CatalogRatio, d.ImpliedRatio, d.Drifted, d.Gap, d.Low, d.High, *tc.want)
			}
		})
	}
}

// A window that filled too little to measure, or reached the limit, or
// opened before turns were read, does not count.
func TestDriftWindowsKeepOnlyMeasurableWindows(t *testing.T) {
	reset := time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)
	w := func(label string, pct float64, r time.Time) QuotaWindow {
		return QuotaWindow{Label: label, UsedPct: pct, ResetsAt: r, Duration: week}
	}
	got := driftWindows([]QuotaReading{
		{At: reset.Add(-48 * time.Hour), Windows: []QuotaWindow{w("weekly", 30, reset), w("weekly Fable", 5, reset)}},
		{At: reset.Add(-24 * time.Hour), Windows: []QuotaWindow{w("weekly", 60, reset.Add(2*time.Minute))}},
		{At: reset.Add(6 * 24 * time.Hour), Windows: []QuotaWindow{w("weekly", 100, reset.Add(week))}},
	})
	if len(got) != 1 || got[0].peakPct != 60 || !got[0].peakAt.Equal(reset.Add(-24*time.Hour)) {
		t.Fatalf("windows = %+v", got)
	}
	in := DriftInput{Readings: nil, Since: reset}
	if TierDrifts(in) != nil {
		t.Error("no readings, yet a comparison")
	}
}

// Requests are priced at list as if metered, and only plan-covered,
// per-request ones of the provider count.
func TestDriftTurnsFrom(t *testing.T) {
	now := time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)
	env := func(provider eventschema.Provider, source eventschema.CostSource, gran string) *eventschema.Envelope {
		e := &eventschema.Envelope{Timestamp: now, Payload: &eventschema.PromptEvent{Provider: provider, RequestModel: "claude-sonnet-5", ResponseModel: "claude-sonnet-5-20261001", CostSource: source}}
		if gran != "" {
			e.Attributes = map[string]string{"granularity": gran}
		}
		return e
	}
	envs := []*eventschema.Envelope{
		env(eventschema.ProviderAnthropic, eventschema.CostSourcePlanIncluded, ""),
		env(eventschema.ProviderAnthropic, eventschema.CostSourceMetered, ""),
		env(eventschema.ProviderOpenAI, eventschema.CostSourcePlanIncluded, ""),
		env(eventschema.ProviderAnthropic, eventschema.CostSourcePlanIncluded, "day"),
	}
	price := func(p *eventschema.PromptEvent, _ time.Time) (float64, error) {
		if p.CostSource != eventschema.CostSourceMetered {
			t.Errorf("priced as %q, want metered", p.CostSource)
		}
		return 2, nil
	}
	got := DriftTurnsFrom(envs, "anthropic", now.Add(-time.Hour), now, price)
	if len(got) != 1 || got[0].ValueUSD != 2 || got[0].Model != "claude-sonnet-5-20261001" {
		t.Fatalf("turns = %+v", got)
	}
}

func TestModelFamily(t *testing.T) {
	for model, want := range map[string]string{
		"claude-opus-5-5":            "opus",
		"anthropic/claude-sonnet-5":  "sonnet",
		"claude-haiku-4-5-20251001":  "haiku",
		"claude-fable-1":             "fable",
		"gpt-6.1-sol":                "gpt-6.1-sol",
		"accounts/fireworks/kimi-k3": "kimi-k3",
	} {
		if got := modelFamily(model); got != want {
			t.Errorf("modelFamily(%q) = %q, want %q", model, got, want)
		}
	}
}
