package claudeusagemeter

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/heartbeat"
)

// clockPoller is a meter polling every five minutes on a clock the test
// moves.
func clockPoller(t *testing.T, m *fakeMeter, bus *captureBus, clock *time.Time) *Poller {
	t.Helper()
	p := NewPoller(bus, PollerOptions{
		SessionKey: "sk",
		Interval:   5 * time.Minute,
		NewClient:  m.client,
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now:        func() time.Time { return *clock },
	})
	if err := p.ensureClient(); err != nil {
		t.Fatal(err)
	}
	return p
}

// An account nobody is using reports the same reading on every poll. It
// was stored once, so after half an hour the meter's newest reading was
// old enough to be read as a meter that had stopped. An unchanged reading
// is stored again every heartbeat, under its own ID and time, and says
// for how long it stands.
func TestPollerKeepsAnUnchangedReadingCurrent(t *testing.T) {
	m := &fakeMeter{orgs: sampleOrgs, usage: map[string]string{"org-abc": enterpriseUsage}}
	bus := &captureBus{}
	start := time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC)
	clock := start
	p := clockPoller(t, m, bus, &clock)

	var stored []time.Duration
	for clock = start; clock.Before(start.Add(31 * time.Minute)); clock = clock.Add(5 * time.Minute) {
		before := bus.PublishedCount()
		p.scan(context.Background())
		if bus.PublishedCount() > before {
			stored = append(stored, clock.Sub(start))
		}
	}
	want := []time.Duration{0, 10 * time.Minute, 20 * time.Minute, 30 * time.Minute}
	if len(stored) != len(want) {
		t.Fatalf("stored at %v, want %v", stored, want)
	}
	ids := map[string]bool{}
	for i, env := range bus.envelopes {
		if stored[i] != want[i] || !env.Timestamp.Equal(start.Add(want[i])) {
			t.Errorf("reading %d stored at %s with time %s, want %s", i, stored[i], env.Timestamp, start.Add(want[i]))
		}
		if d, ok := heartbeat.FreshFor(env.Attributes); !ok || d != 25*time.Minute {
			t.Errorf("reading %d stands for %s (%v), want 25m", i, d, ok)
		}
		if got := heartbeat.IsRepeat(env.Attributes); got != (i > 0) {
			t.Errorf("reading %d: repeat = %v, want %v", i, got, i > 0)
		}
		if env.Attributes["extra_usage_used"] != bus.envelopes[0].Attributes["extra_usage_used"] {
			t.Errorf("reading %d is not the same reading: %v", i, env.Attributes)
		}
		ids[env.ID] = true
	}
	if len(ids) != len(want) {
		t.Errorf("%d IDs for %d readings: the store keeps the first of each ID", len(ids), len(want))
	}
}

// A reading the store did not take is offered again on the next poll. It
// was remembered before it was stored, so a refused one was never stored
// until the account's usage changed.
func TestPollerOffersAgainWhatTheStoreRefused(t *testing.T) {
	m := &fakeMeter{orgs: sampleOrgs, usage: map[string]string{"org-abc": enterpriseUsage}}
	bus := &captureBus{refuse: 1}
	clock := time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC)
	p := clockPoller(t, m, bus, &clock)

	p.scan(context.Background())
	if got := bus.PublishedCount(); got != 0 || p.PublishCount() != 0 {
		t.Fatalf("a refused reading counted as stored: %d in the store, %d published", got, p.PublishCount())
	}
	clock = clock.Add(5 * time.Minute)
	p.scan(context.Background())
	if got := bus.PublishedCount(); got != 1 || p.PublishCount() != 1 {
		t.Fatalf("the same reading was not offered again: %d in the store, %d published", got, p.PublishCount())
	}
	clock = clock.Add(5 * time.Minute)
	p.scan(context.Background())
	if got := bus.PublishedCount(); got != 1 {
		t.Errorf("stored %d times within a heartbeat, want 1", got)
	}
}
