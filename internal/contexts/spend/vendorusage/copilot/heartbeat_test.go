package copilot

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"slices"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/heartbeat"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// clockSource answers with the same quotas on every poll and a
// timestamp_utc that is either the time of the answer or stands still.
type clockSource struct {
	now     func() time.Time
	stands  bool
	percent *float64
}

func (s clockSource) User(context.Context) (*UserResponse, error) {
	at := s.now()
	if s.stands {
		at = time.Date(2026, 11, 10, 6, 0, 0, 0, time.UTC)
	}
	return &UserResponse{
		Login: "test-user", QuotaResetDate: "2026-12-01", TimestampUTC: at.Format(time.RFC3339),
		QuotaSnapshots: map[string]QuotaSnapshot{
			"chat":                 {Entitlement: 300, Remaining: 210, PercentRemaining: s.percent},
			"premium_interactions": {Entitlement: 50, Remaining: 35, PercentRemaining: s.percent},
		},
	}, nil
}

// An account nobody is using reports the same quotas on every poll. Where
// the record's timestamp stood still they were stored once, under that
// timestamp, and read as current for as long as the store kept them;
// where it moved with every answer they were stored on every poll. An
// unchanged set is stored again every heartbeat, whichever the timestamp
// does: under the poll's time, one time for the whole set, with new IDs,
// saying for how long it stands. A set the store took part of is offered
// again whole on the next poll.
func TestPollerKeepsAnUnchangedReadingCurrent(t *testing.T) {
	for _, stands := range []bool{true, false} {
		t.Run(fmt.Sprintf("timestamp stands still: %t", stands), func(t *testing.T) {
			start := time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC)
			at := start
			seventy := 70.0
			bus := &captureBus{refuse: 1}
			p := NewPoller(bus, PollerOptions{
				OAuthToken: "tok",
				NewClient: func(string) UserSource {
					return clockSource{now: func() time.Time { return at }, stands: stands, percent: &seventy}
				},
				Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
				Now:    func() time.Time { return at },
			})

			var stored []time.Duration
			for at = start; at.Before(start.Add(23 * time.Minute)); at = at.Add(2 * time.Minute) {
				before := bus.PublishedCount()
				p.scan(context.Background())
				if bus.PublishedCount() > before {
					stored = append(stored, at.Sub(start))
				}
			}
			// The first poll's first reading was refused: the rest of that
			// set is stored, and the whole set again on the next poll.
			want := []time.Duration{0, 2 * time.Minute, 12 * time.Minute, 22 * time.Minute}
			if !slices.Equal(stored, want) {
				t.Fatalf("stored at %v, want %v", stored, want)
			}

			sets := map[time.Time][]string{}
			ids := map[string]bool{}
			for _, env := range bus.envelopes {
				sets[env.Timestamp] = append(sets[env.Timestamp], env.Attributes["snapshot"])
				ids[env.ID] = true
				// The poller's own interval is 2 minutes: 10 + 3×2.
				if d, ok := heartbeat.FreshFor(env.Attributes); !ok || d != 16*time.Minute {
					t.Errorf("reading at %s stands for %s (%v), want 16m", env.Timestamp, d, ok)
				}
				if env.Attributes["reported_at"] == "" {
					t.Errorf("reading at %s does not say when GitHub dated it", env.Timestamp)
				}
				if pe := env.Payload.(*eventschema.PromptEvent); pe.RequestModel != "" || pe.TotalTokens != 0 {
					t.Errorf("reading names model %q and %d tokens: it would be counted as a request", pe.RequestModel, pe.TotalTokens)
				}
			}
			if len(ids) != len(bus.envelopes) {
				t.Errorf("%d IDs for %d readings: the store keeps the first of each ID", len(ids), len(bus.envelopes))
			}
			whole := []string{"chat", "premium_interactions", PlanSnapshot}
			for _, after := range want[1:] {
				if got := sets[start.Add(after)]; !slices.Equal(got, whole) {
					t.Errorf("set stored at +%s = %v, want %v under the one time", after, got, whole)
				}
			}
			if got := sets[start]; len(got) != 2 {
				t.Errorf("first poll stored %v, want the two readings the store took", got)
			}
		})
	}
}

// A quota that moved is stored on the poll that sees it.
func TestPollerStoresAChangedReadingAtOnce(t *testing.T) {
	at := time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC)
	share := 70.0
	bus := &captureBus{}
	p := NewPoller(bus, PollerOptions{
		OAuthToken: "tok",
		NewClient: func(string) UserSource {
			return clockSource{now: func() time.Time { return at }, stands: true, percent: &share}
		},
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now:    func() time.Time { return at },
	})
	p.scan(context.Background())
	at = at.Add(2 * time.Minute)
	share = 64
	p.scan(context.Background())
	if got := bus.PublishedCount(); got != 6 {
		t.Fatalf("stored %d readings of two different polls, want 6", got)
	}
	if got := bus.envelopes[3].Attributes["percent_remaining"]; got != "64.00" {
		t.Errorf("second set's chat share = %s, want 64.00", got)
	}
}
