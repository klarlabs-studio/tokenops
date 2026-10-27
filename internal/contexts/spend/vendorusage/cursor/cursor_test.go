package cursor

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/pkg/eventschema"
)

const sampleResponse = `{
  "gpt-4": {"numRequests": 120, "maxRequestUsage": 500},
  "gpt-4-32k": {"numRequests": 12, "maxRequestUsage": 50},
  "premiumRequests": {"numRequests": 0, "maxRequestUsage": 0},
  "startOfMonth": "2026-05-01T00:00:00.000Z"
}`

// fakeSource stands in for the HTTP client: it returns resp, or decodes
// body as a legacy usage snapshot.
type fakeSource struct {
	body string
	resp *UsageResponse
}

func (f fakeSource) Usage(context.Context) (*UsageResponse, error) {
	if f.resp != nil {
		return f.resp, nil
	}
	var u UsageResponse
	if err := json.Unmarshal([]byte(f.body), &u); err != nil {
		return nil, err
	}
	return &u, nil
}

type captureBus struct {
	mu        sync.Mutex
	envelopes []*eventschema.Envelope
}

func (b *captureBus) Publish(env *eventschema.Envelope) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.envelopes = append(b.envelopes, env)
}
func (b *captureBus) PublishedCount() int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return int64(len(b.envelopes))
}
func (b *captureBus) DroppedCount() int64         { return 0 }
func (b *captureBus) Close(_ time.Duration) error { return nil }

// Each model row yields one envelope. Same-content second scan must
// dedupe via snapshotKey.
func TestPollerEmitsOneEnvelopePerModelAndDedupes(t *testing.T) {
	bus := &captureBus{}
	p := NewPoller(bus, PollerOptions{
		Cookie:   "tok",
		UserID:   "u",
		Interval: time.Hour,
		NewClient: func(string, string) UsageSource {
			return fakeSource{body: sampleResponse}
		},
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err := p.ensureClient(); err != nil {
		t.Fatal(err)
	}
	p.scan(context.Background())
	if got := bus.PublishedCount(); got != 3 {
		t.Fatalf("want 3 envelopes (one per model row); got %d", got)
	}
	p.scan(context.Background())
	if got := bus.PublishedCount(); got != 3 {
		t.Errorf("same snapshot must dedupe; total %d", got)
	}
}

// newEnvelope encodes the per-model snapshot + used_pct in Attributes
// and tags with ProviderCursor.
func TestNewEnvelopeAttrShape(t *testing.T) {
	env := newEnvelope(
		time.Now().UTC(),
		"user-1",
		"2026-05-01T00:00:00Z",
		"gpt-4",
		ModelUsage{NumRequests: 250, MaxRequestUsage: 500},
	)
	pe := env.Payload.(*eventschema.PromptEvent)
	if pe.Provider != eventschema.ProviderCursor {
		t.Errorf("provider = %s", pe.Provider)
	}
	if env.Source != SourceTag {
		t.Errorf("source = %q", env.Source)
	}
	if env.Attributes["used_pct"] != "50.00" {
		t.Errorf("used_pct = %q", env.Attributes["used_pct"])
	}
	if env.Attributes["num_requests"] != "250" {
		t.Errorf("num_requests = %q", env.Attributes["num_requests"])
	}
}

func (b *captureBus) PublishWait(_ context.Context, env *eventschema.Envelope) error {
	b.Publish(env)
	return nil
}

func scanOnce(t *testing.T, resp *UsageResponse) []*eventschema.Envelope {
	t.Helper()
	bus := &captureBus{}
	p := NewPoller(bus, PollerOptions{
		Cookie: "tok", UserID: "u", Interval: time.Hour,
		NewClient: func(string, string) UsageSource { return fakeSource{resp: resp} },
		Logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	p.scan(context.Background())
	return bus.envelopes
}

func summaryOf(t *testing.T, body string) *Summary {
	t.Helper()
	var s Summary
	if err := json.Unmarshal([]byte(body), &s); err != nil {
		t.Fatal(err)
	}
	return &s
}

// usage-summary yields one reading for the billing cycle: the share of
// the plan's included usage, resetting when the cycle ends.
func TestPollerEmitsTheBillingCycle(t *testing.T) {
	s := summaryOf(t, `{"billingCycleStart":"2026-10-02T09:00:00.000Z","billingCycleEnd":"2026-11-02T09:00:00.000Z",
		"membershipType":"pro","individualUsage":{"plan":{"used":1250,"limit":2000}}}`)
	envs := scanOnce(t, &UsageResponse{Summary: s})
	if len(envs) != 1 {
		t.Fatalf("want one envelope; got %d", len(envs))
	}
	a := envs[0].Attributes
	if a["used_pct"] != "62.50" || a["usage_scope"] != ScopePlan || a["billing_cycle_end"] != "2026-11-02T09:00:00.000Z" ||
		a["used_cents"] != "1250" || a["limit_cents"] != "2000" || a["granularity"] != "monthly_snapshot" {
		t.Errorf("attributes %v", a)
	}
	if envs[0].Payload.(*eventschema.PromptEvent).Provider != eventschema.ProviderCursor {
		t.Error("not reported on cursor")
	}
}

// The weekly pool is written as an account window, and the stale monthly
// figure usage-summary reports beside it is not.
func TestPollerEmitsTheWeeklyPool(t *testing.T) {
	reset := time.Date(2026, 10, 22, 10, 0, 0, 0, time.UTC)
	s := summaryOf(t, `{"billingCycleEnd":"2026-11-01T00:00:00.000Z","membershipType":"enterprise",
		"individualUsage":{"plan":{"used":4000,"limit":4000,"totalPercentUsed":100}}}`)
	envs := scanOnce(t, &UsageResponse{Summary: s, Pool: &WeeklyPool{UsedPct: 10.25, ResetsAt: reset, PlanName: "Premium"}})
	if len(envs) != 1 {
		t.Fatalf("want one envelope; got %d", len(envs))
	}
	a := envs[0].Attributes
	if a["window_0_name"] != "week" || a["window_0_used_pct"] != "10.25" || a["window_0_duration_min"] != "10080" ||
		a["window_0_reset_at"] != "2026-10-22T10:00:00Z" || a["billing"] != "subscription" {
		t.Errorf("attributes %v", a)
	}
	if _, ok := a["used_pct"]; ok {
		t.Errorf("the stale monthly share was written: %v", a)
	}
}

// A legacy row without an allowance carries no share: it was written as
// -1, which the monthly reading took for the plan's.
func TestLegacyRowWithoutAnAllowanceHasNoShare(t *testing.T) {
	env := newEnvelope(time.Now().UTC(), "u", "2026-05-01T00:00:00Z", "premiumRequests", ModelUsage{NumRequests: 3})
	if v, ok := env.Attributes["used_pct"]; ok {
		t.Errorf("used_pct = %q", v)
	}
}
