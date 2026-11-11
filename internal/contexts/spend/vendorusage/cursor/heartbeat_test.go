package cursor

import (
	"context"
	"io"
	"log/slog"
	"slices"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/heartbeat"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// answers is each kind of answer the poller stores a reading of.
func answers(t *testing.T) map[string]*UsageResponse {
	t.Helper()
	cycle := summaryOf(t, `{"billingCycleStart":"2026-10-02T09:00:00.000Z","billingCycleEnd":"2026-12-02T09:00:00.000Z",
		"membershipType":"pro","individualUsage":{"plan":{"used":1250,"limit":2000}}}`)
	return map[string]*UsageResponse{
		"billing cycle": {Summary: cycle},
		"weekly pool":   {Summary: cycle, Pool: &WeeklyPool{UsedPct: 10.25, ResetsAt: time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)}},
		"requests":      {StartOfMonth: "2026-11-01T00:00:00.000Z", Models: map[string]ModelUsage{"gpt-4": {NumRequests: 120, MaxRequestUsage: 500}}},
	}
}

// An account nobody is using reports the same figures on every poll. The
// reading was stored once, and its ID was made from the figures, so even
// a restart could not store it again: its time stayed the time the figures
// were first seen. An unchanged reading is stored again every heartbeat,
// under a new ID and time, says for how long it stands, and names no
// model. A reading the store refused is offered again on the next poll.
func TestPollerKeepsAnUnchangedReadingCurrent(t *testing.T) {
	for name, resp := range answers(t) {
		t.Run(name, func(t *testing.T) {
			start := time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC)
			at := start
			bus := &captureBus{refuse: 1}
			p := NewPoller(bus, PollerOptions{
				Cookie: "tok", UserID: "u",
				NewClient: func(string, string) UsageSource { return fakeSource{resp: resp} },
				Logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
				Now:       func() time.Time { return at },
			})

			var stored []time.Duration
			for at = start; at.Before(start.Add(23 * time.Minute)); at = at.Add(2 * time.Minute) {
				before := bus.PublishedCount()
				p.scan(context.Background())
				if bus.PublishedCount() > before {
					stored = append(stored, at.Sub(start))
				}
			}
			want := []time.Duration{2 * time.Minute, 12 * time.Minute, 22 * time.Minute}
			if !slices.Equal(stored, want) {
				t.Fatalf("stored at %v, want %v", stored, want)
			}
			if got := p.PublishCount(); got != 3 {
				t.Errorf("PublishCount = %d, want 3: a refused reading is not one published", got)
			}
			ids := map[string]bool{}
			for i, env := range bus.envelopes {
				if !env.Timestamp.Equal(start.Add(want[i])) {
					t.Errorf("reading %d carries %s, want %s", i, env.Timestamp, start.Add(want[i]))
				}
				// The poller's own interval is 2 minutes: 10 + 3×2.
				if d, ok := heartbeat.FreshFor(env.Attributes); !ok || d != 16*time.Minute {
					t.Errorf("reading %d stands for %s (%v), want 16m", i, d, ok)
				}
				if pe := env.Payload.(*eventschema.PromptEvent); pe.RequestModel != "" || pe.TotalTokens != 0 {
					t.Errorf("reading %d names model %q and %d tokens: it would be counted as a request", i, pe.RequestModel, pe.TotalTokens)
				}
				ids[env.ID] = true
			}
			if len(ids) != len(want) {
				t.Errorf("%d IDs for %d readings: the store keeps the first of each ID", len(ids), len(want))
			}
		})
	}
}

// Figures that return to an earlier value are a new reading. Its ID was
// made from the figures, so the store took it for the event it already
// had and the newest reading stayed the one before.
func TestAReadingThatReturnsToAnEarlierValueIsStored(t *testing.T) {
	at := time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC)
	pool := &WeeklyPool{UsedPct: 10, ResetsAt: at.Add(72 * time.Hour)}
	bus := &captureBus{}
	p := NewPoller(bus, PollerOptions{
		Cookie: "tok", UserID: "u",
		NewClient: func(string, string) UsageSource { return fakeSource{resp: &UsageResponse{Pool: pool}} },
		Logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now:       func() time.Time { return at },
	})
	for _, pct := range []float64{10, 12, 10} {
		pool.UsedPct = pct
		p.scan(context.Background())
		at = at.Add(2 * time.Minute)
	}
	if len(bus.envelopes) != 3 {
		t.Fatalf("stored %d readings of three different polls", len(bus.envelopes))
	}
	if bus.envelopes[0].ID == bus.envelopes[2].ID {
		t.Error("the reading that returned to 10% has the first one's ID: the store keeps the first")
	}
	if got := bus.envelopes[2].Attributes["window_0_used_pct"]; got != "10.00" {
		t.Errorf("newest reading = %s, want 10.00", got)
	}
}
