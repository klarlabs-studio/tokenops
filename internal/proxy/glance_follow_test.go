package proxy

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/headroom"
	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/contexts/coaching/waste"
	"go.klarlabs.de/tokenops/internal/contexts/spend/plans"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// figuresUntil is the earliest moment the figures can read differently
// with no event arriving: a reset, an expiry or a freshness limit they
// show, the prompt cache turning to expiring, a midnight, or the max age.
func TestFiguresUntil(t *testing.T) {
	at := time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)
	window := func(resets time.Time) headroom.Glance {
		return headroom.Glance{Headroom: headroom.Result{Reports: []plans.HeadroomReport{{
			Provider: "anthropic",
			Windows:  []plans.VendorWindow{{Name: "5h", ResetsAt: resets, ObservedAt: at.Add(-time.Minute)}},
		}}}}
	}
	cache := func(expires time.Time) headroom.Glance {
		c := waste.Countdown(expires.Add(-5*time.Minute), 5*time.Minute, at)
		return headroom.Glance{PromptCache: &c}
	}
	// "Today" ends at the next midnight, here and in UTC.
	next := func(loc *time.Location) time.Time {
		y, m, d := at.In(loc).Date()
		return time.Date(y, m, d+1, 0, 0, 0, 0, loc)
	}
	midnight := next(time.UTC)
	if local := next(time.Local); local.Before(midnight) {
		midnight = local
	}
	cases := []struct {
		name   string
		g      headroom.Glance
		maxAge time.Duration
		want   time.Time
	}{
		{name: "the max age when nothing comes sooner", maxAge: 5 * time.Minute, want: at.Add(5 * time.Minute)},
		{name: "a window that resets first", g: window(at.Add(2 * time.Minute)), maxAge: 5 * time.Minute, want: at.Add(2 * time.Minute)},
		{name: "a past reset is no boundary", g: window(at.Add(-time.Minute)), maxAge: 5 * time.Minute, want: at.Add(5 * time.Minute)},
		{name: "the prompt cache turning to expiring", g: cache(at.Add(3 * time.Minute)),
			maxAge: 5 * time.Minute, want: at.Add(3*time.Minute - waste.CacheExpiringWithin)},
		{name: "a midnight before the max age", maxAge: 48 * time.Hour, want: midnight},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := figuresUntil(glanceFigures{Glance: c.g, At: at}, c.maxAge); !got.Equal(c.want) {
				t.Errorf("figuresUntil = %s, want %s", got, c.want)
			}
		})
	}
}

// countingEvents is planEvents that counts its reads.
type countingEvents struct {
	planEvents
	reads *atomic.Int32
}

func (c countingEvents) ReadEvents(ctx context.Context, t eventschema.EventType, since time.Time) ([]*eventschema.Envelope, error) {
	c.reads.Add(1)
	return c.planEvents.ReadEvents(ctx, t, since)
}

// The glance's figures are read from the events again only when the
// inputs moved or their max age passed; in between the glance answers the
// figures it has, with the time they were computed for.
func TestGlanceFiguresFollowTheInputs(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	at := time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)
	var reads atomic.Int32
	cfg := &config.Config{Plans: map[string]string{"anthropic": "claude-ultra"}}
	deps := func() headroom.Deps {
		return headroom.Deps{Config: cfg, Reader: countingEvents{reads: &reads}}
	}
	pos := "1"
	inputs := func(context.Context) (string, bool) { return pos, true }
	s := New("127.0.0.1:0", WithPlans(deps), WithGlanceFollows(inputs, 5*time.Minute))
	steps := []struct {
		name     string
		now      time.Time
		pos      string
		computes bool
		computed time.Time
	}{
		{name: "first", now: at, pos: "1", computes: true, computed: at},
		{name: "nothing moved", now: at.Add(time.Minute), pos: "1", computed: at},
		{name: "an event arrived", now: at.Add(2 * time.Minute), pos: "2", computes: true, computed: at.Add(2 * time.Minute)},
		{name: "quiet again", now: at.Add(6 * time.Minute), pos: "2", computed: at.Add(2 * time.Minute)},
		{name: "past the max age", now: at.Add(7 * time.Minute), pos: "2", computes: true, computed: at.Add(7 * time.Minute)},
	}
	for _, st := range steps {
		pos = st.pos
		before := reads.Load()
		v, err := s.computeView(context.Background(), st.now)
		if err != nil {
			t.Fatalf("%s: %v", st.name, err)
		}
		if read := reads.Load() > before; read != st.computes {
			t.Errorf("%s: read the events %v, want %v", st.name, read, st.computes)
		}
		if !v.Glance.ComputedAt.Equal(st.computed) {
			t.Errorf("%s: computed_at %s, want %s", st.name, v.Glance.ComputedAt, st.computed)
		}
	}
}
