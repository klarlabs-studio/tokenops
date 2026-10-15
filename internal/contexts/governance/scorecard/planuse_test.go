package scorecard

import (
	"context"
	"strings"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/pkg/eventschema"
)

type envsReader []*eventschema.Envelope

func (r envsReader) ReadEvents(_ context.Context, t eventschema.EventType, since time.Time) ([]*eventschema.Envelope, error) {
	var out []*eventschema.Envelope
	for _, e := range r {
		if e.Type == t && !e.Timestamp.Before(since) {
			out = append(out, e)
		}
	}
	return out, nil
}

// The scorecard says, per provider, how much of the plan was used and how
// much of the usage the plan carried: the "are the seats used?" figures.
func TestScorecardCarriesPlanUse(t *testing.T) {
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	turn := func(cs eventschema.CostSource, tokens int64) *eventschema.Envelope {
		return &eventschema.Envelope{Type: eventschema.EventTypePrompt, Source: "claude-code-jsonl", Timestamp: now.Add(-time.Hour),
			Attributes: map[string]string{"granularity": "assistant_turn", "session_id": "s1"},
			Payload: &eventschema.PromptEvent{Provider: eventschema.ProviderAnthropic, CostSource: cs, TotalTokens: tokens,
				InputTokens: tokens, SessionID: "s1"}}
	}
	reading := &eventschema.Envelope{Type: eventschema.EventTypePrompt, Source: "claude-usage-meter", Timestamp: now.Add(-3 * 24 * time.Hour),
		Attributes: map[string]string{"granularity": "quota_snapshot", "seven_day_kind": "weekly_all", "seven_day_used_pct": "64.00",
			"seven_day_reset_at": now.Add(-2 * 24 * time.Hour).Format(time.RFC3339)},
		Payload: &eventschema.PromptEvent{Provider: eventschema.ProviderAnthropic}}
	r := envsReader{reading, turn(eventschema.CostSourcePlanIncluded, 930), turn(eventschema.CostSourceMetered, 70)}
	sc := Build(context.Background(), r, BuildParams{ClockNow: func() time.Time { return now }, SACPctOverride: 95})
	a, ok := sc.PlanUse.For("anthropic")
	if !ok || a.UtilizationPct == nil || *a.UtilizationPct != 64 || a.CoveragePct == nil || *a.CoveragePct != 93 {
		t.Fatalf("plan use = %+v", sc.PlanUse)
	}
	text := sc.String()
	for _, want := range []string{"Plan use (last 7 days)", "anthropic", "utilization 64% (weekly, 1 window)", "coverage 93%"} {
		if !strings.Contains(text, want) {
			t.Errorf("scorecard text missing %q:\n%s", want, text)
		}
	}
	b, err := sc.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"plan_use"`) || !strings.Contains(string(b), `"utilization_pct": 64`) {
		t.Errorf("JSON missing plan use: %s", b)
	}
}
