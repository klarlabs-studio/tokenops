package headroom_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/headroom"
	"go.klarlabs.de/tokenops/internal/contexts/spend/plans"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// adviceEvents is four weeks peaking at peak percent of each weekly
// window, and 28 plan-covered turns.
func adviceEvents(now time.Time, peak string) []*eventschema.Envelope {
	envs := make([]*eventschema.Envelope, 0, 36)
	since := now.Add(-28 * 24 * time.Hour)
	reading := func(at, reset time.Time, used string) *eventschema.Envelope {
		return &eventschema.Envelope{Source: "claude-usage-meter", Timestamp: at, Attributes: map[string]string{
			"granularity":    "quota_snapshot",
			"seven_day_kind": "weekly_all", "seven_day_used_pct": used, "seven_day_reset_at": reset.Format(time.RFC3339),
		}, Payload: &eventschema.PromptEvent{Provider: eventschema.ProviderAnthropic}}
	}
	for i := range 4 {
		reset := since.Add(time.Duration(i+1) * 7 * 24 * time.Hour)
		start := reset.Add(-7 * 24 * time.Hour)
		envs = append(envs, reading(start.Add(24*time.Hour), reset, "5"), reading(start.Add(5*24*time.Hour), reset, peak))
	}
	for i := range 28 {
		envs = append(envs, &eventschema.Envelope{Source: "claude-code-jsonl", Timestamp: since.Add(time.Duration(i)*24*time.Hour + time.Hour),
			Attributes: map[string]string{"granularity": "assistant_turn"},
			Payload:    &eventschema.PromptEvent{Provider: eventschema.ProviderAnthropic, CostSource: eventschema.CostSourcePlanIncluded}})
	}
	return envs
}

func tenDollars(*eventschema.PromptEvent, time.Time) (float64, error) { return 10, nil }

func TestAdviseReplaysTheBoundPlan(t *testing.T) {
	now := time.Date(2026, 10, 14, 0, 0, 0, 0, time.UTC)
	// Four weeks on Max peaking at 15% of the week: three quarters of a Pro
	// week, which Pro would have carried.
	history := plans.History{{Provider: "anthropic", Plan: "claude-max", From: now.Add(-90 * 24 * time.Hour), Price: 90, Currency: "USD"}}
	deps := headroom.Deps{
		Config:      cfgWith(map[string]string{"anthropic": "claude-max", "openai": "gpt-plus"}),
		Reader:      &eventsReader{envs: adviceEvents(now, "15")},
		Price:       tenDollars,
		PlanHistory: func() (plans.History, error) { return history, nil },
	}
	res, err := headroom.Advise(context.Background(), deps, headroom.AdviceQuery{}, now)
	if err != nil {
		t.Fatal(err)
	}
	if res.Days != plans.AdviceDays || len(res.Providers) != 1 {
		t.Fatalf("result = %+v", res)
	}
	a := res.Providers[0]
	var cur plans.Option
	for _, o := range a.Options {
		if o.Current {
			cur = o
		}
	}
	// The operator's own price wins over the list price.
	if cur.Plan != "claude-max" || cur.MonthlyUSD == nil || *cur.MonthlyUSD != 90 || cur.PriceSource != "yours" {
		t.Errorf("current = %+v", cur)
	}
	if a.ListValueUSD == nil || *a.ListValueUSD != 280 {
		t.Errorf("list value = %v, want 28 turns at $10", a.ListValueUSD)
	}
	r := a.Recommendation
	if r == nil || r.Verdict != plans.VerdictSwitch || r.Plan != "claude-pro" || r.SavingsUSD != 70 {
		t.Fatalf("recommendation = %+v", r)
	}
	if !a.WorthSaying() {
		t.Error("a $70 saving with no lockouts is worth a glance finding")
	}
	// A plan with no modelled multiple is named, not dropped.
	if len(res.Notes) != 1 || !strings.Contains(res.Notes[0], "gpt-plus") {
		t.Errorf("notes = %q", res.Notes)
	}
	// Advice never rebinds: the config is untouched.
	if deps.Config.Plans["anthropic"] != "claude-max" {
		t.Error("the advice changed the binding")
	}
	b, err := json.Marshal(res.Payload())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"providers"`, `"confidence":"medium"`, `"verdict":"switch"`, `"lockouts":0`, "unverified"} {
		if !strings.Contains(string(b), want) {
			t.Errorf("payload missing %s: %s", want, b)
		}
	}
}

// Ultra has no 5-hour limit, so its readings cannot say how often Max or
// Pro would have run out within 5 hours: the advice says so, is low
// confidence, and is not put on the glance.
func TestAdviceOnUltraCannotSeeFiveHourDemand(t *testing.T) {
	now := time.Date(2026, 10, 14, 0, 0, 0, 0, time.UTC)
	// An unverified account may still see a 5-hour window: at 90% it says
	// nothing about what Max's 5-hour allowance would have carried.
	envs := adviceEvents(now, "20")
	at := now.Add(-3 * 24 * time.Hour)
	envs = append(envs, &eventschema.Envelope{Source: "claude-usage-meter", Timestamp: at, Attributes: map[string]string{
		"granularity":    "quota_snapshot",
		"five_hour_kind": "session", "five_hour_used_pct": "90", "five_hour_reset_at": at.Add(time.Hour).Format(time.RFC3339),
	}, Payload: &eventschema.PromptEvent{Provider: eventschema.ProviderAnthropic}})
	deps := headroom.Deps{
		Config: cfgWith(map[string]string{"anthropic": "claude-ultra"}),
		Reader: &eventsReader{envs: envs},
		Price:  tenDollars,
	}
	res, err := headroom.Advise(context.Background(), deps, headroom.AdviceQuery{}, now)
	if err != nil || len(res.Providers) != 1 {
		t.Fatalf("result = %+v, %v", res, err)
	}
	a := res.Providers[0]
	var max *plans.Option
	for i, o := range a.Options {
		if o.Plan == "claude-max" && o.Kind == plans.OptionPlan {
			max = &a.Options[i]
		}
	}
	// 20% of Ultra's week is two Pro weeks, 40% of Max's.
	if max == nil || max.PeakPct == nil || *max.PeakPct != 40 || max.PeakWindow != "weekly" || max.Lockouts != 0 {
		t.Errorf("Max = %+v", max)
	}
	if a.Confidence != plans.ConfidenceLow || a.WorthSaying() {
		t.Errorf("confidence %q, worth saying %v; want low and kept off the glance", a.Confidence, a.WorthSaying())
	}
	if !strings.Contains(strings.Join(a.Caveats, " "), "Claude Ultra has no 5-hour limit") {
		t.Errorf("caveats = %q", a.Caveats)
	}
}

func TestAdviseUnanswered(t *testing.T) {
	now := time.Date(2026, 10, 14, 0, 0, 0, 0, time.UTC)
	res, err := headroom.Advise(context.Background(), headroom.Deps{Config: cfgWith(nil), Reader: &eventsReader{}}, headroom.AdviceQuery{}, now)
	if err != nil {
		t.Fatal(err)
	}
	if p := res.Payload(); p.Error != headroom.ErrPlansUnconfigured {
		t.Errorf("no plans = %+v", p)
	}
	res, err = headroom.Advise(context.Background(), headroom.Deps{Config: cfgWith(map[string]string{"anthropic": "claude-pro"})}, headroom.AdviceQuery{}, now)
	if err != nil {
		t.Fatal(err)
	}
	if p := res.Payload(); p.Error != headroom.ErrStorageDisabled {
		t.Errorf("no store = %+v", p)
	}
	if _, err := headroom.Advise(context.Background(), headroom.Deps{}, headroom.AdviceQuery{Days: headroom.MaxAdviceDays + 1}, now); err == nil {
		t.Error("a period past the longest is refused")
	}
}

// The glance carries the advice, read at most once an hour through the
// cache the daemon holds.
func TestGlanceCarriesPlanAdvice(t *testing.T) {
	now := time.Date(2026, 10, 14, 0, 0, 0, 0, time.UTC)
	reader := &countingReader{eventsReader: eventsReader{envs: adviceEvents(now, "15")}}
	deps := headroom.Deps{
		Config:      cfgWith(map[string]string{"anthropic": "claude-max"}),
		Reader:      reader,
		Price:       tenDollars,
		AdviceCache: headroom.NewAdviceCache(),
	}
	g, err := headroom.ComputeGlance(context.Background(), deps, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(g.PlanAdvice) != 1 || !g.PlanAdvice[0].WorthSaying() {
		t.Fatalf("glance advice = %+v", g.PlanAdvice)
	}
	if p := g.Payload(); len(p.PlanAdvice) != 1 {
		t.Errorf("payload advice = %+v", p.PlanAdvice)
	}
	before := reader.long
	if _, err := headroom.ComputeGlance(context.Background(), deps, now.Add(30*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if reader.long != before {
		t.Errorf("the second glance within the hour read %d month-long windows again", reader.long-before)
	}
}

// countingReader counts reads reaching back further than three weeks.
type countingReader struct {
	eventsReader
	long int
}

func (r *countingReader) ReadEvents(ctx context.Context, t eventschema.EventType, since time.Time) ([]*eventschema.Envelope, error) {
	if len(r.envs) > 0 && since.Before(r.envs[len(r.envs)-1].Timestamp.Add(-21*24*time.Hour)) {
		r.long++
	}
	return r.eventsReader.ReadEvents(ctx, t, since)
}
