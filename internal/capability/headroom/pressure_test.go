package headroom_test

import (
	"context"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/headroom"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// promptReader holds plan-included prompts and nothing else.
type promptReader struct{ envs []*eventschema.Envelope }

func (r promptReader) ReadEvents(_ context.Context, t eventschema.EventType, _ time.Time) ([]*eventschema.Envelope, error) {
	if t != eventschema.EventTypePrompt {
		return nil, nil
	}
	return r.envs, nil
}

func (r promptReader) CountBySource(context.Context, time.Time, time.Time) (map[string]int64, error) {
	return map[string]int64{"proxy": int64(len(r.envs))}, nil
}

func messages(n int, at time.Time) []*eventschema.Envelope {
	out := make([]*eventschema.Envelope, 0, n)
	for range n {
		out = append(out, &eventschema.Envelope{
			Type: eventschema.EventTypePrompt, Timestamp: at, Source: "proxy",
			Payload: &eventschema.PromptEvent{Provider: eventschema.ProviderAnthropic, CostSource: eventschema.CostSourcePlanIncluded},
		})
	}
	return out
}

func TestWindowPressureFromMessagesInTheWindow(t *testing.T) {
	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	cfg := cfgWith(map[string]string{"anthropic": "claude-pro"})
	pct, ok := headroom.WindowPressure(context.Background(), cfg, promptReader{messages(9, now.Add(-time.Hour))}, eventschema.ProviderAnthropic, now)
	if !ok || pct != 20 {
		t.Errorf("pressure %.1f%% (known %v), want 9 of claude-pro's 45 = 20%%", pct, ok)
	}
}

// Unknown, never zero, whenever the window cannot be read: a router rule
// gated on pressure must not fire on a default.
func TestWindowPressureIsUnknownWithoutAReading(t *testing.T) {
	now := time.Now()
	cases := map[string]struct {
		cfg    map[string]string
		reader headroom.Reader
	}{
		"no plan bound":        {cfg: map[string]string{}, reader: promptReader{}},
		"no rate-limit window": {cfg: map[string]string{"anthropic": "pay-as-you-go"}, reader: promptReader{}},
		"unknown plan":         {cfg: map[string]string{"anthropic": "claude-ultra"}, reader: promptReader{}},
		"no event store":       {cfg: map[string]string{"anthropic": "claude-pro"}},
	}
	for name, tc := range cases {
		if pct, ok := headroom.WindowPressure(context.Background(), cfgWith(tc.cfg), tc.reader, eventschema.ProviderAnthropic, now); ok {
			t.Errorf("%s: pressure %.1f%%, want unknown", name, pct)
		}
	}
	if _, ok := headroom.WindowPressure(context.Background(), nil, promptReader{}, eventschema.ProviderAnthropic, now); ok {
		t.Error("nil config: want unknown")
	}
}
