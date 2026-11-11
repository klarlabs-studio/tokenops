package fireworks

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/heartbeat"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// storeBus keeps what it is handed, after refusing the first refuse
// readings.
type storeBus struct {
	got    []*eventschema.Envelope
	refuse int
}

func (b *storeBus) Publish(env *eventschema.Envelope) { b.got = append(b.got, env) }
func (b *storeBus) PublishWait(_ context.Context, env *eventschema.Envelope) error {
	if b.refuse > 0 {
		b.refuse--
		return errors.New("the store is busy")
	}
	b.got = append(b.got, env)
	return nil
}
func (b *storeBus) DroppedCount() int64       { return 0 }
func (b *storeBus) PublishedCount() int64     { return int64(len(b.got)) }
func (b *storeBus) Close(time.Duration) error { return nil }

// An account with no spend since the last poll reports the same reading.
// It is stored again every heartbeat, under a new ID and time, and says
// for how long it stands; a reading the store refused is offered again on
// the next poll.
func TestPollerKeepsAnUnchangedReadingCurrent(t *testing.T) {
	api := &fakeAPI{stamp: "k1", readResult: Reading{AccountID: "acme", Scope: ScopeUser, UsedUSD: 12, LimitUSD: 50}}
	clock := now
	bus := &storeBus{refuse: 1}
	p := NewPoller(bus, PollerOptions{Client: api, Home: t.TempDir(), UserID: "u-ada", Interval: 5 * time.Minute, Now: func() time.Time { return clock }})

	var stored []time.Duration
	for clock = now; clock.Before(now.Add(26 * time.Minute)); clock = clock.Add(5 * time.Minute) {
		before := len(bus.got)
		p.Scan(context.Background())
		if len(bus.got) > before {
			stored = append(stored, clock.Sub(now))
		}
	}
	want := []time.Duration{5 * time.Minute, 15 * time.Minute, 25 * time.Minute}
	if !slices.Equal(stored, want) {
		t.Fatalf("stored at %v, want %v", stored, want)
	}
	ids := map[string]bool{}
	for i, env := range bus.got {
		if !env.Timestamp.Equal(now.Add(want[i])) {
			t.Errorf("reading %d carries %s, want %s", i, env.Timestamp, now.Add(want[i]))
		}
		if d, ok := heartbeat.FreshFor(env.Attributes); !ok || d != 25*time.Minute {
			t.Errorf("reading %d stands for %s (%v), want 25m", i, d, ok)
		}
		ids[env.ID] = true
	}
	if len(ids) != len(want) {
		t.Errorf("%d IDs for %d readings: the store keeps the first of each ID", len(ids), len(want))
	}
}

// Spend that moved is stored on the poll that reads it, not at the next
// heartbeat.
func TestPollerStoresAChangedReadingAtOnce(t *testing.T) {
	api := &fakeAPI{stamp: "k1", readResult: Reading{AccountID: "acme", Scope: ScopeUser, UsedUSD: 12, LimitUSD: 50}}
	clock := now
	bus := &storeBus{}
	p := NewPoller(bus, PollerOptions{Client: api, Home: t.TempDir(), UserID: "u-ada", Interval: 5 * time.Minute, Now: func() time.Time { return clock }})

	p.Scan(context.Background())
	clock = now.Add(time.Minute)
	api.readResult.UsedUSD = 13
	p.Scan(context.Background())
	if len(bus.got) != 2 || bus.got[1].Attributes["extra_usage_used"] != "13.00" || !bus.got[1].Timestamp.Equal(clock) {
		t.Fatalf("stored %d readings, want the changed one a minute later", len(bus.got))
	}
	clock = now.Add(2 * time.Minute)
	p.Scan(context.Background())
	if len(bus.got) != 2 {
		t.Errorf("an unchanged reading stored again a minute later: %d readings", len(bus.got))
	}
}
