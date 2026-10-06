package headroom_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/headroom"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

type fakeAttrs struct {
	attrs   map[string]string
	at      time.Time
	err     error
	sources []string
	since   time.Time
	// only, when set, is the one source holding the reading.
	only string
}

func (f *fakeAttrs) LatestAttributesBySource(_ context.Context, source, _ string, since time.Time) (map[string]string, time.Time, bool, error) {
	f.sources, f.since = append(f.sources, source), since
	if f.only != "" && source != f.only {
		return nil, time.Time{}, false, nil
	}
	if f.err != nil {
		return nil, time.Time{}, false, f.err
	}
	if f.attrs == nil || f.at.Before(since) {
		return nil, time.Time{}, false, nil
	}
	return f.attrs, f.at, true, nil
}

func TestLiveWindowPicksTheMostConstrained(t *testing.T) {
	now := time.Now()
	r := &fakeAttrs{at: now.Add(-3 * time.Minute), attrs: map[string]string{
		"five_hour_kind": "session", "five_hour_used_pct": "12.00", "five_hour_reset_at": now.Add(2 * time.Hour).Format(time.RFC3339Nano),
		"seven_day_kind": "weekly_all", "seven_day_used_pct": "81.00", "seven_day_reset_at": now.Add(48 * time.Hour).Format(time.RFC3339Nano),
	}}
	w, ok := headroom.LiveWindow(context.Background(), *cfgWith(map[string]string{"anthropic": "claude-max-20x"}), r, eventschema.ProviderAnthropic, now)
	if !ok || w.Label != "weekly" || w.UsedPct != 81 {
		t.Fatalf("LiveWindow = %+v, %v; want the 81%% weekly window", w, ok)
	}
	if len(r.sources) != 3 || r.sources[0] != "claude-usage-meter" || r.sources[1] != "claude-code-statusline" || r.sources[2] != "claude-code-oauth" ||
		now.Sub(r.since) != headroom.LiveFreshness {
		t.Errorf("read %q since %v; want the claude.ai meter, the status line and Claude Code's sign-in within the freshness bound", r.sources, now.Sub(r.since))
	}
}

// Claude Code's status line alone is enough: no claude.ai login needed.
func TestLiveWindowFromTheStatusLineAlone(t *testing.T) {
	now := time.Now()
	r := &fakeAttrs{only: "claude-code-statusline", at: now.Add(-time.Minute), attrs: map[string]string{
		"five_hour_used_pct": "64.00", "five_hour_reset_at": now.Add(time.Hour).Format(time.RFC3339),
		"granularity": "quota_snapshot",
	}}
	w, ok := headroom.LiveWindow(context.Background(), *cfgWith(map[string]string{"anthropic": "claude-max-20x"}), r, eventschema.ProviderAnthropic, now)
	if !ok || w.Label != "5-hour" || w.UsedPct != 64 {
		t.Fatalf("LiveWindow = %+v, %v; want the status line's 64%% 5-hour window", w, ok)
	}
}

func TestLiveWindowFallsBack(t *testing.T) {
	now := time.Now()
	attrs := map[string]string{"five_hour_kind": "session", "five_hour_used_pct": "50.00", "five_hour_reset_at": now.Add(time.Hour).Format(time.RFC3339Nano)}
	bound := *cfgWith(map[string]string{"anthropic": "claude-max-20x"})
	for name, tc := range map[string]struct {
		plans    map[string]string
		r        headroom.AttributeReader
		provider eventschema.Provider
	}{
		"no plan":         {nil, &fakeAttrs{attrs: attrs, at: now}, eventschema.ProviderAnthropic},
		"unknown plan":    {map[string]string{"anthropic": "claude-ultra"}, &fakeAttrs{attrs: attrs, at: now}, eventschema.ProviderAnthropic},
		"stale reading":   {bound.Plans, &fakeAttrs{attrs: attrs, at: now.Add(-2 * time.Hour)}, eventschema.ProviderAnthropic},
		"read error":      {bound.Plans, &fakeAttrs{err: errors.New("busy")}, eventschema.ProviderAnthropic},
		"no reader":       {bound.Plans, nil, eventschema.ProviderAnthropic},
		"no window meter": {map[string]string{"cursor": "cursor-pro"}, &fakeAttrs{attrs: attrs, at: now}, eventschema.ProviderCursor},
	} {
		if w, ok := headroom.LiveWindow(context.Background(), *cfgWith(tc.plans), tc.r, tc.provider, now); ok {
			t.Errorf("%s: LiveWindow = %+v, want no window so the caller falls back", name, w)
		}
	}
}
