package headroom_test

import (
	"context"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/headroom"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

type meterReading struct {
	source string
	at     time.Time
	attrs  map[string]string
}

// fakeHistory holds a series of meter readings, as the store does.
type fakeHistory struct{ readings []meterReading }

func (f *fakeHistory) LatestAttributesBySource(ctx context.Context, source, key string, since time.Time) (map[string]string, time.Time, bool, error) {
	return f.AttributesBySourceBetween(ctx, source, key, since, time.Unix(1<<40, 0))
}

func (f *fakeHistory) AttributesBySourceBetween(_ context.Context, source, key string, since, until time.Time) (map[string]string, time.Time, bool, error) {
	var best *meterReading
	for i := range f.readings {
		r := &f.readings[i]
		if _, has := r.attrs[key]; !has || r.source != source || r.at.Before(since) || r.at.After(until) {
			continue
		}
		if best == nil || r.at.After(best.at) {
			best = r
		}
	}
	if best == nil {
		return nil, time.Time{}, false, nil
	}
	return best.attrs, best.at, true, nil
}

func fiveHourAt(used string, reset time.Time) map[string]string {
	return map[string]string{"five_hour_kind": "session", "five_hour_used_pct": used, "five_hour_reset_at": reset.Format(time.RFC3339)}
}

// The look-backs read the store as it stood an hour and a quarter hour
// before the newest reading.
func TestBurnReadsTheLookbacksFromHistory(t *testing.T) {
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	reset := now.Add(2 * time.Hour)
	r := &fakeHistory{readings: []meterReading{
		{"claude-usage-meter", now.Add(-65 * time.Minute), fiveHourAt("30.00", reset)},
		{"claude-usage-meter", now.Add(-20 * time.Minute), fiveHourAt("50.00", reset)},
		{"claude-usage-meter", now.Add(-2 * time.Minute), fiveHourAt("60.00", reset)},
	}}
	cfg := *cfgWith(map[string]string{"anthropic": "claude-ultra"})
	cur, v := headroom.Burn(context.Background(), cfg, r, eventschema.ProviderAnthropic, now)
	if len(cur.Windows) != 1 || !cur.At.Equal(now.Add(-2*time.Minute)) {
		t.Fatalf("current reading = %+v", cur)
	}
	if v.Stale || len(v.Warnings) != 1 || v.Warnings[0].Window.Label != "5-hour" {
		t.Fatalf("verdict = %+v, want the 5-hour window to run out", v)
	}

	// The same window, idle for the last half hour: no warning.
	r.readings[2].attrs = fiveHourAt("50.00", reset)
	if _, v := headroom.Burn(context.Background(), cfg, r, eventschema.ProviderAnthropic, now); len(v.Warnings) != 0 {
		t.Fatalf("idle window warned: %+v", v)
	}
}

func TestBurnStaleReading(t *testing.T) {
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	reset := now.Add(2 * time.Hour)
	r := &fakeHistory{readings: []meterReading{
		{"claude-usage-meter", now.Add(-90 * time.Minute), fiveHourAt("20.00", reset)},
		{"claude-usage-meter", now.Add(-25 * time.Minute), fiveHourAt("60.00", reset)},
	}}
	cfg := *cfgWith(map[string]string{"anthropic": "claude-ultra"})
	if _, v := headroom.Burn(context.Background(), cfg, r, eventschema.ProviderAnthropic, now); !v.Stale || len(v.Warnings) != 0 {
		t.Fatalf("a 25-minute-old reading forecast: %+v", v)
	}
}

// A reading can be minutes old and still be of a window that has ended.
// Its share is the last window's: it is neither the live window nor
// something to forecast from.
func TestAWindowThatResetSinceItsReadingIsNotLive(t *testing.T) {
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	cfg := *cfgWith(map[string]string{"anthropic": "claude-ultra"})
	ended := now.Add(-5 * time.Minute)
	attrs := fiveHourAt("95.00", ended)
	r := &fakeHistory{readings: []meterReading{{"claude-usage-meter", now.Add(-10 * time.Minute), attrs}}}

	if w, ok := headroom.LiveWindow(context.Background(), cfg, r, eventschema.ProviderAnthropic, now); ok {
		t.Errorf("live window %+v: it reset five minutes ago", w)
	}
	if cur, v := headroom.Burn(context.Background(), cfg, r, eventschema.ProviderAnthropic, now); len(cur.Windows) != 0 || len(v.Warnings) != 0 {
		t.Errorf("burn read %+v %+v from a window that has ended", cur, v)
	}

	// The week in the same reading is still running, and is what is live.
	attrs["seven_day_kind"], attrs["seven_day_used_pct"], attrs["seven_day_reset_at"] = "weekly", "40.00", now.Add(48*time.Hour).Format(time.RFC3339)
	if w, ok := headroom.LiveWindow(context.Background(), cfg, r, eventschema.ProviderAnthropic, now); !ok || w.UsedPct != 40 {
		t.Errorf("live window %+v %v, want the week at 40%%", w, ok)
	}
	if cur, _ := headroom.Burn(context.Background(), cfg, r, eventschema.ProviderAnthropic, now); len(cur.Windows) != 1 || cur.Windows[0].UsedPct != 40 {
		t.Errorf("burn read %+v, want the week alone", cur)
	}
}
