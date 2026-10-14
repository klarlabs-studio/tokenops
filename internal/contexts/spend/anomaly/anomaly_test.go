package anomaly

import (
	"math"
	"testing"
	"time"
)

var now = time.Date(2026, 10, 15, 9, 0, 0, 0, time.UTC)

// day is n days before today, as a UTC midnight.
func day(n int) time.Time {
	return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, -n)
}

// steady is days of ordinary usage before yesterday: about $10 a day of
// sonnet across two sessions in one repository, with a little noise.
func steady(days int) []Item {
	var out []Item
	for n := 2; n < 2+days; n++ {
		v := 10 + float64(n%3) - 1
		out = append(out,
			Item{Day: day(n), Provider: "anthropic", Model: "sonnet", Repo: "tokenops", Session: "s" + string(rune('a'+n%26)), Tool: "claude-code-jsonl", PlanCovered: true, Tokens: int64(v * 1e5 / 2), ValueUSD: v / 2},
			Item{Day: day(n), Provider: "anthropic", Model: "sonnet", Repo: "warden", Session: "t" + string(rune('a'+n%26)), Tool: "claude-code-jsonl", PlanCovered: true, Tokens: int64(v * 1e5 / 2), ValueUSD: v / 2},
		)
	}
	return out
}

func near(a, b float64) bool { return math.Abs(a-b) < 0.01 }

func TestNothingIsFlaggedBeforeTenDaysOfHistory(t *testing.T) {
	items := append(steady(8), Item{Day: day(1), Provider: "anthropic", Model: "opus", Session: "x", Tokens: 1e6, ValueUSD: 80, PlanCovered: true})
	if got := Detect(items, now, Config{}); len(got) != 0 {
		t.Fatalf("flagged with 8 days of history: %+v", got)
	}
}

func TestAnOrdinaryDayIsNotFlagged(t *testing.T) {
	items := append(steady(20), Item{Day: day(1), Provider: "anthropic", Model: "sonnet", Session: "x", Tokens: 1e6, ValueUSD: 11, PlanCovered: true})
	if got := Detect(items, now, Config{}); len(got) != 0 {
		t.Fatalf("an ordinary day was flagged: %+v", got)
	}
}

func TestASmallSpikeIsUnderTheEffectFloor(t *testing.T) {
	var items []Item
	for n := 2; n < 22; n++ {
		items = append(items, Item{Day: day(n), Provider: "openai", Model: "gpt", Session: "s", Tokens: 1000, CostUSD: 0.10, ValueUSD: 0.10})
	}
	items = append(items, Item{Day: day(1), Provider: "openai", Model: "gpt", Session: "s", Tokens: 9000, CostUSD: 0.90, ValueUSD: 0.90})
	if got := Detect(items, now, Config{}); len(got) != 0 {
		t.Fatalf("a $0.80 rise was flagged: %+v", got)
	}
}

func TestAPricierModelDefaultIsNamed(t *testing.T) {
	items := append(steady(20),
		// Yesterday: the usual sonnet volume moved to opus at 5x the price.
		Item{Day: day(1), Provider: "anthropic", Model: "opus", Repo: "tokenops", Session: "y1", Tool: "claude-code-jsonl", PlanCovered: true, Tokens: 1e6, ValueUSD: 50},
	)
	got := Detect(items, now, Config{})
	if len(got) != 1 {
		t.Fatalf("got %d anomalies, want 1: %+v", len(got), got)
	}
	a := got[0]
	if a.Metric != MetricUsage || !a.Day.Equal(day(1)) || a.Partial {
		t.Errorf("anomaly = %s on %s partial=%v", a.Metric, a.Day, a.Partial)
	}
	if !near(a.ActualUSD, 50) || a.ExcessUSD < 30 || a.HistoryDays < 10 {
		t.Errorf("figures: %+v", a)
	}
	if a.Cause.Kind != CausePricierModel || a.Cause.Value != "opus" {
		t.Fatalf("cause = %+v, want pricier model opus", a.Cause)
	}
	if a.Cause.Pattern != "pricier_model:opus" {
		t.Errorf("pattern = %q", a.Cause.Pattern)
	}
	if a.ID != "anomaly-usage-2026-10-14" {
		t.Errorf("id = %q", a.ID)
	}
	// The model is the top contributor and explains all of the rise.
	top := a.Contributors[0]
	if top.Dimension != DimModel || top.Value != "opus" || !near(top.ActualUSD, 50) || top.Share < 0.99 {
		t.Errorf("top contributor = %+v", top)
	}
	for i := 1; i < len(a.Contributors); i++ {
		if a.Contributors[i].DeltaUSD > a.Contributors[i-1].DeltaUSD {
			t.Fatalf("contributors not ranked by impact: %+v", a.Contributors)
		}
	}
}

func TestUsageBilledPerTokenUnderAPlanIsNamed(t *testing.T) {
	items := append(steady(20),
		// Yesterday a misrouted key billed Claude per token.
		Item{Day: day(1), Provider: "anthropic", Endpoint: "anthropic-api", Model: "sonnet", Repo: "tokenops", Session: "k1", Tool: "claude-code-jsonl", Tokens: 2e6, CostUSD: 24, ValueUSD: 24},
	)
	got := Detect(items, now, Config{PlanProviders: map[string]bool{"anthropic": true}})
	if len(got) != 1 {
		t.Fatalf("got %d anomalies, want the spend one: %+v", len(got), got)
	}
	a := got[0]
	if a.Metric != MetricSpend {
		t.Errorf("metric = %s; spend wins over usage on the same day", a.Metric)
	}
	if a.Cause.Kind != CauseMeteredOnPlan || a.Cause.Value != "anthropic" {
		t.Fatalf("cause = %+v", a.Cause)
	}
	if a.Cause.Endpoint != "anthropic-api" {
		t.Errorf("endpoint = %q", a.Cause.Endpoint)
	}
	if a.Cause.CoveragePct == nil || a.Cause.BaselineCoveragePct == nil || *a.Cause.CoveragePct > 50 || *a.Cause.BaselineCoveragePct < 99 {
		t.Errorf("coverage = %v from %v", a.Cause.CoveragePct, a.Cause.BaselineCoveragePct)
	}
}

func TestOneSessionCarryingTheRiseIsALikelyLoop(t *testing.T) {
	items := append(steady(20),
		Item{Day: day(1), Provider: "anthropic", Model: "sonnet", Repo: "warden", Session: "loop", Tool: "claude-code-jsonl", PlanCovered: true, Tokens: 6e6, ValueUSD: 60},
	)
	got := Detect(items, now, Config{})
	if len(got) != 1 {
		t.Fatalf("got %+v", got)
	}
	c := got[0].Cause
	if c.Kind != CauseRunawayLoop || c.Value != "loop" || c.Repo != "warden" {
		t.Fatalf("cause = %+v", c)
	}
	if c.Pattern != "runaway_loop:warden" {
		t.Errorf("pattern = %q", c.Pattern)
	}
}

func TestTodaySoFarIsFlaggedOnceItPassesTheBand(t *testing.T) {
	items := append(steady(20),
		Item{Day: day(1), Provider: "anthropic", Model: "sonnet", Session: "y", PlanCovered: true, Tokens: 1e6, ValueUSD: 10},
	)
	for _, s := range []string{"a1", "a2", "a3", "a4"} {
		items = append(items, Item{Day: day(0), Provider: "anthropic", Model: "sonnet", Repo: "tokenops", Session: s, PlanCovered: true, Tokens: 1e6, ValueUSD: 10})
	}
	got := Detect(items, now, Config{})
	if len(got) != 1 || !got[0].Partial || !got[0].Day.Equal(day(0)) {
		t.Fatalf("got %+v, want today's partial day", got)
	}
	if got[0].Cause.Kind != CauseSpike || got[0].Cause.Pattern != "spike:usage:model=sonnet" {
		t.Errorf("cause = %+v", got[0].Cause)
	}
}
