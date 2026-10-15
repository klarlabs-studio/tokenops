package scorecard

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/pkg/eventschema"
)

func TestCompareAppliesPolarity(t *testing.T) {
	for _, c := range []struct {
		name      string
		cur, base float64
		pol       Polarity
		tol       float64
		want      Direction
	}{
		{"higher better, up", 95, 80, HigherIsBetter, 1, DirectionBetter},
		{"higher better, down", 70, 80, HigherIsBetter, 1, DirectionWorse},
		{"lower better, down", 5, 15, LowerIsBetter, 1, DirectionBetter},
		{"lower better, up", 12, 6, LowerIsBetter, 1, DirectionWorse},
		{"within tolerance", 80.6, 80, HigherIsBetter, 1, DirectionUnchanged},
		{"neutral up", 64, 52, Neutral, 1, DirectionUp},
		{"neutral down", 40, 52, Neutral, 1, DirectionDown},
		{"neutral flat", 52.4, 52, Neutral, 1, DirectionUnchanged},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := compare(c.cur, c.base, c.pol, c.tol)
			if got.Direction != c.want {
				t.Errorf("direction = %s, want %s", got.Direction, c.want)
			}
			if got.Baseline != c.base {
				t.Errorf("baseline = %v, want %v", got.Baseline, c.base)
			}
		})
	}
}

// chrTurn is one per-session prompt whose cache share is cached/100.
func chrTurn(at time.Time, session string, cached int64) *eventschema.Envelope {
	pe := &eventschema.PromptEvent{Provider: eventschema.ProviderAnthropic, SessionID: session,
		InputTokens: 100, CachedInputTokens: cached, TotalTokens: 100}
	return &eventschema.Envelope{Type: eventschema.EventTypePrompt, Source: "claude-code-jsonl", Timestamp: at, Payload: pe}
}

// Each KPI is set against the operator's own four weeks before the
// period, and says whether the move is better or worse given which way
// is healthy for it. The baseline never leaks into the period's figure.
func TestBuildComparesKPIsWithOwnBaseline(t *testing.T) {
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	r := envsReader{
		chrTurn(now.Add(-time.Hour), "s1", 95),
		chrTurn(now.AddDate(0, 0, -10), "s0", 80),
		// Outside the baseline too: not counted anywhere.
		chrTurn(now.AddDate(0, 0, -40), "old", 0),
	}
	sc := Build(context.Background(), r, BuildParams{
		ClockNow: func() time.Time { return now },
		AgentKPIs: AgentKPIInputs{
			ConfirmationGateRatePct: 5, ConfirmationGateComputed: true,
			RegenerateRatePct: 12, RegenerateComputed: true,
			ToolSuccessRatePct: 96, ToolSuccessComputed: true,
		},
		AgentKPIBaseline: AgentKPIInputs{
			ConfirmationGateRatePct: 15, ConfirmationGateComputed: true,
			RegenerateRatePct: 6, RegenerateComputed: true,
			ToolSuccessRatePct: 95.6, ToolSuccessComputed: true,
		},
	})
	if sc.CacheHitRatio.Value != 95 {
		t.Fatalf("CHR = %v, want 95 (the period alone)", sc.CacheHitRatio.Value)
	}
	chr := sc.CacheHitRatio.VsBaseline
	if chr == nil || chr.Baseline != 80 || chr.Delta != 15 || chr.Direction != DirectionBetter {
		t.Fatalf("CHR vs baseline = %+v", chr)
	}
	if sc.CacheHitRatio.Polarity != HigherIsBetter {
		t.Errorf("CHR polarity = %q", sc.CacheHitRatio.Polarity)
	}
	if c := sc.ConfirmationGateRate.VsBaseline; c == nil || c.Direction != DirectionBetter {
		t.Errorf("CGR fell from 15%% to 5%%: %+v", c)
	}
	if c := sc.RegenerateRate.VsBaseline; c == nil || c.Direction != DirectionWorse {
		t.Errorf("RGR rose from 6%% to 12%%: %+v", c)
	}
	if c := sc.ToolSuccessRate.VsBaseline; c == nil || c.Direction != DirectionUnchanged {
		t.Errorf("TCS moved 0.4 points: %+v", c)
	}
	if sc.DestructiveRate.VsBaseline != nil {
		t.Error("an unmeasured KPI has nothing to compare")
	}
	if sc.SelfBaseline == nil || sc.SelfBaseline.Days != 28 ||
		!sc.SelfBaseline.Until.Equal(now.AddDate(0, 0, -7)) || !sc.SelfBaseline.Since.Equal(now.AddDate(0, 0, -35)) {
		t.Fatalf("self baseline = %+v", sc.SelfBaseline)
	}
	text := sc.String()
	for _, want := range []string{
		"Against your own baseline: the 28 days before",
		"95.0 [A] · baseline 80.0 (+15.0, better)",
		"5.0 [A] · baseline 15.0 (-10.0, better)",
		"12.0 [C] · baseline 6.0 (+6.0, worse)",
		"(+0.4, unchanged)",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("text missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "no baseline captured") {
		t.Errorf("the --baseline-ref label must not deny the baseline shown:\n%s", text)
	}
	b, err := sc.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]json.RawMessage
	if err := json.Unmarshal(b, &wire); err != nil {
		t.Fatal(err)
	}
	if _, ok := wire["self_baseline"]; !ok {
		t.Errorf("JSON missing self_baseline: %s", b)
	}
	if !strings.Contains(string(wire["cache_hit_ratio"]), `"vs_baseline"`) ||
		!strings.Contains(string(wire["cache_hit_ratio"]), `"direction": "better"`) {
		t.Errorf("JSON CHR missing comparison: %s", wire["cache_hit_ratio"])
	}
}

// An operator's own figure replaces the measurement, so there is nothing
// of theirs to compare it with; and with no history before the period
// there is no baseline at all.
func TestBuildLeavesComparisonOutWithoutBaseline(t *testing.T) {
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	r := envsReader{
		chrTurn(now.Add(-time.Hour), "s1", 95),
		chrTurn(now.AddDate(0, 0, -10), "s0", 80),
	}
	sc := Build(context.Background(), r, BuildParams{ClockNow: func() time.Time { return now }, SACPctOverride: 70})
	if sc.SpendAttribution.VsBaseline != nil {
		t.Errorf("an override is not compared: %+v", sc.SpendAttribution.VsBaseline)
	}
	fresh := Build(context.Background(), envsReader{chrTurn(now.Add(-time.Hour), "s1", 95)},
		BuildParams{ClockNow: func() time.Time { return now }})
	if fresh.CacheHitRatio.VsBaseline != nil || fresh.SelfBaseline != nil {
		t.Errorf("no history before the period, no baseline: %+v %+v", fresh.CacheHitRatio.VsBaseline, fresh.SelfBaseline)
	}
	if strings.Contains(fresh.String(), "Against your own baseline") {
		t.Errorf("no baseline line without one:\n%s", fresh.String())
	}
}

// Plan use is set against the same baseline, without a verdict: a seat
// used more is neither better nor worse.
func TestBuildComparesPlanUseWithoutVerdict(t *testing.T) {
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	turn := func(at time.Time, cs eventschema.CostSource, tokens int64) *eventschema.Envelope {
		return &eventschema.Envelope{Type: eventschema.EventTypePrompt, Source: "claude-code-jsonl", Timestamp: at,
			Attributes: map[string]string{"granularity": "assistant_turn", "session_id": "s1"},
			Payload: &eventschema.PromptEvent{Provider: eventschema.ProviderAnthropic, CostSource: cs, TotalTokens: tokens,
				InputTokens: tokens, SessionID: "s1"}}
	}
	r := envsReader{
		turn(now.Add(-time.Hour), eventschema.CostSourcePlanIncluded, 930),
		turn(now.Add(-time.Hour), eventschema.CostSourceMetered, 70),
		turn(now.AddDate(0, 0, -10), eventschema.CostSourcePlanIncluded, 500),
		turn(now.AddDate(0, 0, -10), eventschema.CostSourceMetered, 500),
	}
	sc := Build(context.Background(), r, BuildParams{ClockNow: func() time.Time { return now }})
	if len(sc.PlanUseVsBaseline) != 1 {
		t.Fatalf("plan use vs baseline = %+v", sc.PlanUseVsBaseline)
	}
	c := sc.PlanUseVsBaseline[0]
	if c.Provider != "anthropic" || c.Coverage == nil || c.Coverage.Baseline != 50 || c.Coverage.Direction != DirectionUp {
		t.Fatalf("coverage vs baseline = %+v", c.Coverage)
	}
	if !strings.Contains(sc.String(), "coverage 93% of tokens on a plan (baseline 50%, up)") {
		t.Errorf("text missing plan-use baseline:\n%s", sc.String())
	}
}

// Unit costs already carry their baseline; cheaper is better.
func TestBuildGivesUnitCostsADirection(t *testing.T) {
	down, up, flat, base := -30.0, 30.0, 2.0, 10.0
	sc := Build(context.Background(), nil, BuildParams{SACPctOverride: 95, UnitCosts: []UnitCost{
		{Unit: "merged PR", Units: 2, PerUnitUSD: 7, BaselinePerUnitUSD: &base, ChangePct: &down},
		{Unit: "commit", Units: 2, PerUnitUSD: 13, BaselinePerUnitUSD: &base, ChangePct: &up},
		{Unit: "achieved outcome", Units: 1, PerUnitUSD: 10.2, BaselinePerUnitUSD: &base, ChangePct: &flat},
	}})
	want := map[string]Direction{"merged PR": DirectionBetter, "commit": DirectionWorse, "achieved outcome": DirectionUnchanged}
	for _, u := range sc.UnitCosts {
		if u.Direction != want[u.Unit] {
			t.Errorf("%s direction = %q, want %q", u.Unit, u.Direction, want[u.Unit])
		}
	}
	if !strings.Contains(sc.String(), "baseline $10.00 (-30%, better)") {
		t.Errorf("text missing unit-cost direction:\n%s", sc.String())
	}
}
