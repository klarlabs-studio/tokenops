package headroom_test

import (
	"context"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/headroom"
	"go.klarlabs.de/tokenops/internal/contexts/spend/plans"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// meterStore serves the same meter readings as events and as history, as
// the store does.
type meterStore struct {
	eventsReader
	fakeHistory
}

// Glance and the coach forecast a window the same way. Three hours into
// a five-hour window at 60%, the average lasts to the reset; the last
// hour used 30 points, which runs out in about 80 minutes. The coach
// warned from the last hour while glance said it lasts; glance's windows
// now say what the coach says.
func TestGlanceForecastsWhatTheCoachWarns(t *testing.T) {
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	reset := now.Add(2 * time.Hour)
	var s meterStore
	for _, r := range []struct {
		ago  time.Duration
		used string
	}{{65 * time.Minute, "30.00"}, {20 * time.Minute, "52.00"}, {2 * time.Minute, "60.00"}} {
		at := now.Add(-r.ago)
		attrs := fiveHourAt(r.used, reset)
		attrs["granularity"] = "quota_snapshot"
		s.readings = append(s.readings, meterReading{"claude-usage-meter", at, attrs})
		s.envs = append(s.envs, &eventschema.Envelope{Source: "claude-usage-meter", Timestamp: at, Attributes: attrs,
			Payload: &eventschema.PromptEvent{Provider: eventschema.ProviderAnthropic}})
	}
	cfg := cfgWith(map[string]string{"anthropic": "claude-ultra"})
	_, burn := headroom.Burn(context.Background(), *cfg, &s, eventschema.ProviderAnthropic, now)
	if len(burn.Warnings) != 1 {
		t.Fatalf("fixture: the coach should warn, got %+v", burn)
	}

	res, err := headroom.Compute(context.Background(), headroom.Deps{Config: cfg, Reader: &s}, now)
	if err != nil || len(res.Reports) != 1 || len(res.Reports[0].Windows) == 0 {
		t.Fatalf("headroom = %+v, %v", res, err)
	}
	p := res.Reports[0].Windows[0].Pace
	if p == nil || p.LastsToReset || p.Basis != plans.BasisRecent || p.RunsOutIn <= 0 || p.RunsOutIn > 2*time.Hour {
		t.Errorf("glance's pace %+v, want it running out at the last hour's rate as the coach warns", p)
	}

	// The session budget's windows say the same.
	b, err := headroom.SessionBudgets(context.Background(), headroom.Deps{Config: cfg, Reader: &s}, now)
	if err != nil || len(b.Budgets) != 1 || len(b.Budgets[0].Windows) == 0 {
		t.Fatalf("budgets = %+v, %v", b, err)
	}
	if p := b.Budgets[0].Windows[0].Pace; p == nil || p.Basis != plans.BasisRecent || p.LastsToReset {
		t.Errorf("the session budget's pace %+v", p)
	}

	// A reader that cannot read earlier readings keeps the average.
	res, err = headroom.Compute(context.Background(), headroom.Deps{Config: cfg, Reader: &s.eventsReader}, now)
	if err != nil || len(res.Reports) != 1 {
		t.Fatalf("headroom = %+v, %v", res, err)
	}
	if p := res.Reports[0].Windows[0].Pace; p == nil || p.Basis != "" {
		t.Errorf("without history: %+v", p)
	}
}
