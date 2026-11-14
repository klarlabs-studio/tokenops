package codexappserver

import (
	"context"
	"strings"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/heartbeat"
)

// An idle account reports the same windows on every poll. Every limit is
// stored again each heartbeat, under a new ID and time, and says for how
// long it stands.
func TestPollerKeepsUnchangedLimitsCurrent(t *testing.T) {
	start := time.Date(2026, 10, 28, 9, 0, 0, 0, time.UTC)
	at := start
	dial, _ := fakeServer(t, twoLimits)
	bus := &recordingBus{}
	p := NewPoller(bus, PollerOptions{Dial: dial, Interval: 5 * time.Minute, Now: func() time.Time { return at }})

	var stored []time.Duration
	for at = start; at.Before(start.Add(21 * time.Minute)); at = at.Add(5 * time.Minute) {
		before := len(bus.got)
		p.Scan(context.Background())
		if n := len(bus.got) - before; n == 2 {
			stored = append(stored, at.Sub(start))
		} else if n != 0 {
			t.Fatalf("at %s: stored %d readings, want both limits or neither", at.Sub(start), n)
		}
	}
	if len(stored) != 3 || stored[1] != 10*time.Minute || stored[2] != 20*time.Minute {
		t.Fatalf("stored at %v, want 0s, 10m and 20m", stored)
	}
	ids := map[string]bool{}
	for i, env := range bus.got {
		if want := start.Add(stored[i/2]); !env.Timestamp.Equal(want) {
			t.Errorf("reading %d carries %s, want %s", i, env.Timestamp, want)
		}
		if d, ok := heartbeat.FreshFor(env.Attributes); !ok || d != 25*time.Minute {
			t.Errorf("reading %d stands for %s (%v), want 25m", i, d, ok)
		}
		if got := heartbeat.IsRepeat(env.Attributes); got != (i >= 2) {
			t.Errorf("reading %d: repeat = %v, want %v: only the heartbeats repeat", i, got, i >= 2)
		}
		ids[env.ID] = true
	}
	if len(ids) != len(bus.got) {
		t.Errorf("%d IDs for %d readings: the store keeps the first of each ID", len(ids), len(bus.got))
	}
}

// When the store takes one limit and refuses the other, the next scan
// offers both again: the set was remembered as stored before it was, and
// the refused limit then stayed unstored until a window changed.
func TestPollerOffersTheSetAgainWhenOneLimitWasRefused(t *testing.T) {
	at := time.Date(2026, 10, 28, 9, 0, 0, 0, time.UTC)
	dial, _ := fakeServer(t, twoLimits)
	bus := &recordingBus{refuse: 1}
	p := NewPoller(bus, PollerOptions{Dial: dial, Now: func() time.Time { return at }})

	p.Scan(context.Background())
	if len(bus.got) != 1 {
		t.Fatalf("stored %d readings with one refused, want 1", len(bus.got))
	}
	at = at.Add(time.Minute)
	p.Scan(context.Background())
	if len(bus.got) != 3 {
		t.Fatalf("stored %d readings after the retry, want 3: the one taken and both again", len(bus.got))
	}
	limits := map[string]bool{}
	for _, env := range bus.got[1:] {
		limits[env.Attributes["limit_id"]] = true
	}
	if len(limits) != 2 {
		t.Errorf("the retry stored limits %v, want both", limits)
	}
	at = at.Add(time.Minute)
	p.Scan(context.Background())
	if len(bus.got) != 3 {
		t.Errorf("stored again within a heartbeat: %d readings", len(bus.got))
	}
}

// A limit whose share moved is stored on the scan that reads it, with the
// rest of its set, not at the next heartbeat.
func TestPollerStoresChangedLimitsAtOnce(t *testing.T) {
	at := time.Date(2026, 10, 28, 9, 0, 0, 0, time.UTC)
	before, _ := fakeServer(t, twoLimits)
	after, _ := fakeServer(t, strings.ReplaceAll(twoLimits, `"usedPercent":40`, `"usedPercent":41`))
	dial := before
	bus := &recordingBus{}
	p := NewPoller(bus, PollerOptions{Dial: func(ctx context.Context) (Conn, error) { return dial(ctx) }, Now: func() time.Time { return at }})

	p.Scan(context.Background())
	at, dial = at.Add(time.Minute), after
	p.Scan(context.Background())
	if len(bus.got) != 4 {
		t.Fatalf("stored %d readings, want both limits twice", len(bus.got))
	}
	if got := bus.got[2].Attributes["primary_used_pct"]; got != "41.00" || !bus.got[2].Timestamp.Equal(at) {
		t.Errorf("the changed limit is stored as %s at %s", got, bus.got[2].Timestamp)
	}
	at = at.Add(time.Minute)
	p.Scan(context.Background())
	if len(bus.got) != 4 {
		t.Errorf("unchanged limits stored again a minute later: %d readings", len(bus.got))
	}
}
