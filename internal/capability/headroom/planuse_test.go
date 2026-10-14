package headroom_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/headroom"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

type eventsReader struct{ envs []*eventschema.Envelope }

func (r *eventsReader) ReadEvents(_ context.Context, _ eventschema.EventType, since time.Time) ([]*eventschema.Envelope, error) {
	var out []*eventschema.Envelope
	for _, e := range r.envs {
		if !e.Timestamp.Before(since) {
			out = append(out, e)
		}
	}
	return out, nil
}

func (r *eventsReader) CountBySource(_ context.Context, _, _ time.Time) (map[string]int64, error) {
	return nil, nil
}

// The glance (and so the daemon's /api/glance the menu bar reads) carries
// each provider's plan utilization and coverage over the last week.
func TestGlanceCarriesPlanUse(t *testing.T) {
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	turn := func(cs eventschema.CostSource, tokens int64) *eventschema.Envelope {
		return &eventschema.Envelope{Source: "claude-code-jsonl", Timestamp: now.Add(-time.Hour),
			Attributes: map[string]string{"granularity": "assistant_turn"},
			Payload:    &eventschema.PromptEvent{Provider: eventschema.ProviderAnthropic, CostSource: cs, TotalTokens: tokens}}
	}
	week := now.Add(-2 * 24 * time.Hour).Format(time.RFC3339)
	reading := &eventschema.Envelope{Source: "claude-usage-meter", Timestamp: now.Add(-3 * 24 * time.Hour),
		Attributes: map[string]string{"granularity": "quota_snapshot", "seven_day_kind": "weekly_all", "seven_day_used_pct": "48.00", "seven_day_reset_at": week},
		Payload:    &eventschema.PromptEvent{Provider: eventschema.ProviderAnthropic}}
	deps := headroom.Deps{
		Config: cfgWith(map[string]string{"anthropic": "claude-max-20x"}),
		Reader: &eventsReader{envs: []*eventschema.Envelope{reading, turn(eventschema.CostSourcePlanIncluded, 750), turn(eventschema.CostSourceMetered, 250)}},
	}
	g, err := headroom.ComputeGlance(context.Background(), deps, now)
	if err != nil {
		t.Fatal(err)
	}
	a, ok := g.PlanUse.For("anthropic")
	if !ok || a.UtilizationPct == nil || *a.UtilizationPct != 48 || a.CoveragePct == nil || *a.CoveragePct != 75 {
		t.Fatalf("plan use = %+v", g.PlanUse)
	}
	b, err := json.Marshal(g.Payload())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"plan_use"`, `"utilization_pct":48`, `"coverage_pct":75`, `"utilization_window":"weekly"`} {
		if !strings.Contains(string(b), want) {
			t.Errorf("payload missing %s: %s", want, b)
		}
	}
}
