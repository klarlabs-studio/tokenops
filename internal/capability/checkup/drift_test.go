package checkup

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/spend/plans"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

type envReader []*eventschema.Envelope

func (r envReader) ReadEvents(_ context.Context, _ eventschema.EventType, since time.Time) ([]*eventschema.Envelope, error) {
	var out []*eventschema.Envelope
	for _, e := range r {
		if !e.Timestamp.Before(since) {
			out = append(out, e)
		}
	}
	return out, nil
}

// driftEvents is ten weekly windows of Sonnet work, five on Pro and five
// on Max, each filled to 40%: one percent held $1 on Pro and maxPerPct on
// Max.
func driftEvents(start time.Time, maxPerPct float64) envReader {
	envs := make(envReader, 0, 20)
	for i := range 10 {
		open := start.Add(time.Duration(i) * 7 * 24 * time.Hour)
		reset := open.Add(7 * 24 * time.Hour)
		envs = append(envs, &eventschema.Envelope{Source: "claude-code-statusline", Timestamp: open.Add(3 * 24 * time.Hour),
			Attributes: map[string]string{"granularity": "quota_snapshot",
				"seven_day_kind": "weekly_all", "seven_day_used_pct": "40", "seven_day_reset_at": reset.Format(time.RFC3339)},
			Payload: &eventschema.PromptEvent{Provider: eventschema.ProviderAnthropic}})
		perPct := 1.0
		if i >= 5 {
			perPct = maxPerPct
		}
		envs = append(envs, &eventschema.Envelope{Source: "claude-code-jsonl", Timestamp: open.Add(time.Hour),
			Attributes: map[string]string{"granularity": "assistant_turn", "usd": strconv.FormatFloat(perPct*40, 'f', -1, 64)},
			Payload: &eventschema.PromptEvent{Provider: eventschema.ProviderAnthropic, RequestModel: "claude-sonnet-5",
				CostSource: eventschema.CostSourcePlanIncluded}})
	}
	return envs
}

// The checkup reads the store's readings and requests over the plan
// history and raises a finding only when the tiers' ratio drifted.
func TestCatalogDrift(t *testing.T) {
	now := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	start := now.AddDate(0, 0, -80)
	switchAt := start.Add(5 * 7 * 24 * time.Hour)
	history := plans.History{
		{Provider: "anthropic", Plan: "claude-pro", From: start.AddDate(0, 0, -30)},
		{Provider: "anthropic", Plan: "claude-max", From: switchAt},
	}
	var envs envReader
	// The rate card prices each request at the value its event carries.
	price := func(_ *eventschema.PromptEvent, at time.Time) (float64, error) {
		for _, e := range envs {
			if e.Timestamp.Equal(at) && e.Attributes["usd"] != "" {
				return strconv.ParseFloat(e.Attributes["usd"], 64)
			}
		}
		return 0, nil
	}
	opts := func(r envReader) Options {
		envs = r
		return Options{Now: now, Events: r, Plans: map[string]string{"anthropic": "claude-max"}, PlanHistory: history, Price: price}
	}

	got := catalogDrift(context.Background(), opts(driftEvents(start, 2.5)))
	if len(got) != 1 {
		t.Fatalf("findings = %+v", got)
	}
	f := got[0]
	if f.Kind != "catalog-drift" || f.Level != LevelNotice ||
		!strings.Contains(f.Title, "put Claude Max at 2.5x Claude Pro; the catalog says 5x") ||
		!strings.Contains(f.Evidence, "5 weekly windows on Claude Pro and 5 on Claude Max") ||
		!strings.Contains(f.Evidence, "50% below") || !strings.Contains(f.Evidence, "sonnet") ||
		!strings.Contains(f.Fix, "ADR 0002") {
		t.Errorf("finding = %+v", f)
	}
	if got := catalogDrift(context.Background(), opts(driftEvents(start, 3.5))); len(got) != 1 || got[0].Level != LevelInfo {
		t.Errorf("a 30%% gap is info: %+v", got)
	}
	if got := catalogDrift(context.Background(), opts(driftEvents(start, 4))); got != nil {
		t.Errorf("a 20%% gap is inside the threshold, yet %+v", got)
	}
	if got := catalogDrift(context.Background(), opts(driftEvents(start, 5))); got != nil {
		t.Errorf("agreeing tiers raised %+v", got)
	}
	if got := catalogDrift(context.Background(), Options{Now: now, Plans: map[string]string{"anthropic": "claude-max"}}); got != nil {
		t.Errorf("no store, yet %+v", got)
	}
}
