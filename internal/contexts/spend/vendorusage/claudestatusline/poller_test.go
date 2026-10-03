package claudestatusline

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/infra/claudelimits"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

type recordingBus struct {
	mu  sync.Mutex
	got []*eventschema.Envelope
}

func (b *recordingBus) Publish(env *eventschema.Envelope) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.got = append(b.got, env)
}

func (b *recordingBus) PublishWait(_ context.Context, env *eventschema.Envelope) error {
	b.Publish(env)
	return nil
}

func (b *recordingBus) DroppedCount() int64       { return 0 }
func (b *recordingBus) PublishedCount() int64     { return int64(len(b.got)) }
func (b *recordingBus) Close(time.Duration) error { return nil }

func f(v float64) *float64 { return &v }

func TestEnvelopeUsesTheMeterShape(t *testing.T) {
	at := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	env := Envelope(claudelimits.Reading{
		ObservedAt: at,
		FiveHour:   &claudelimits.Window{UsedPct: 23.5, ResetsAt: at.Add(2 * time.Hour).Unix()},
		SevenDay:   &claudelimits.Window{UsedPct: 41.2, ResetsAt: at.Add(72 * time.Hour).Unix()},
	})
	if env == nil || env.Source != SourceTag || !env.Timestamp.Equal(at) {
		t.Fatalf("envelope = %+v", env)
	}
	want := map[string]string{
		"five_hour_used_pct": "23.50",
		"five_hour_reset_at": "2026-10-03T14:00:00Z",
		"seven_day_used_pct": "41.20",
		"granularity":        "quota_snapshot",
	}
	for k, v := range want {
		if env.Attributes[k] != v {
			t.Errorf("%s = %q, want %q", k, env.Attributes[k], v)
		}
	}
	if _, ok := env.Attributes["extra_usage_limit"]; ok {
		t.Error("a subscriber's windows produced a spend reading")
	}
	if p := env.Payload.(*eventschema.PromptEvent); p.Provider != eventschema.ProviderAnthropic {
		t.Errorf("provider = %q", p.Provider)
	}
}

func TestSpendLimitDollarsOnlyForAMonthlyLimit(t *testing.T) {
	at := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name      string
		win       claudelimits.Window
		wantSpend bool
	}{
		{"monthly with dollars", claudelimits.Window{UsedPct: 62.8, UsedUSD: f(314.12), LimitUSD: f(500), Period: "monthly"}, true},
		{"weekly with dollars", claudelimits.Window{UsedPct: 62.8, UsedUSD: f(314.12), LimitUSD: f(500), Period: "weekly"}, false},
		{"percentage only", claudelimits.Window{UsedPct: 62.8}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := tc.win
			env := Envelope(claudelimits.Reading{ObservedAt: at, SpendLimit: &w})
			if env.Attributes["spend_limit_used_pct"] != "62.80" {
				t.Fatalf("spend limit window missing: %v", env.Attributes)
			}
			_, gotSpend := env.Attributes["extra_usage_limit"]
			if gotSpend != tc.wantSpend {
				t.Fatalf("spend reading = %v, want %v", gotSpend, tc.wantSpend)
			}
			if tc.wantSpend && (env.Attributes["extra_usage_used"] != "314.12" || env.Attributes["extra_usage_currency"] != "USD") {
				t.Fatalf("spend = %v", env.Attributes)
			}
		})
	}
}

func TestPollerStoresChangesAndAHeartbeat(t *testing.T) {
	path := filepath.Join(t.TempDir(), "claude-limits.json")
	bus := &recordingBus{}
	p := NewPoller(bus, PollerOptions{Path: path})
	at := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	reset := at.Add(3 * time.Hour).Unix()
	write := func(at time.Time, pct float64) {
		t.Helper()
		if err := claudelimits.Write(path, claudelimits.Reading{ObservedAt: at, FiveHour: &claudelimits.Window{UsedPct: pct, ResetsAt: reset}}); err != nil {
			t.Fatal(err)
		}
	}
	ctx := context.Background()

	p.once(ctx) // no file yet
	write(at, 10)
	p.once(ctx)
	p.once(ctx) // same reading
	write(at.Add(time.Minute), 10)
	p.once(ctx) // newer, unchanged, inside the heartbeat
	write(at.Add(2*time.Minute), 12)
	p.once(ctx) // changed
	write(at.Add(20*time.Minute), 12)
	p.once(ctx) // unchanged, past the heartbeat

	if len(bus.got) != 3 {
		t.Fatalf("stored %d readings, want 3", len(bus.got))
	}
}
