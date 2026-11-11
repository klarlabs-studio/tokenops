package cursor

import (
	"context"
	"encoding/json"
	"errors"
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
	// refuse is how many PublishWait calls to refuse before taking one.
	refuse int
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

// The legacy answer yields one reading of the plan's request allowance:
// the row that has one, the busiest when several do. It carries no model,
// so it is not counted as a request to one, and is stored once while the
// figures stand.
func TestPollerEmitsTheRequestAllowance(t *testing.T) {
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
	p.scan(context.Background())
	if got := bus.PublishedCount(); got != 1 {
		t.Fatalf("stored %d readings of one unchanged answer, want 1", got)
	}
	env := bus.envelopes[0]
	a := env.Attributes
	// gpt-4 is at 120 of 500 and gpt-4-32k at 12 of 50: both 24%, and the
	// first by name is named. premiumRequests has no allowance.
	if a["used_pct"] != "24.00" || a["allowance_of"] != "gpt-4" || a["num_requests"] != "120" || a["max_request_usage"] != "500" ||
		a["granularity"] != "monthly_snapshot" || a["start_of_month"] != "2026-05-01T00:00:00.000Z" {
		t.Errorf("attributes %v", a)
	}
	pe := env.Payload.(*eventschema.PromptEvent)
	if pe.Provider != eventschema.ProviderCursor || pe.RequestModel != "" || pe.TotalTokens != 0 {
		t.Errorf("payload %+v: a reading has a provider, no model and no tokens", pe)
	}
	if env.Source != SourceTag {
		t.Errorf("source = %q", env.Source)
	}
}

// The busiest row with an allowance is the plan's share; a row without
// one has no share, and an answer with none is no reading.
func TestRequestsEnvelopeReadsTheBusiestAllowance(t *testing.T) {
	at := time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC)
	env := requestsEnvelope(at, "u", &UsageResponse{Models: map[string]ModelUsage{
		"gpt-4":           {NumRequests: 250, MaxRequestUsage: 500},
		"gpt-4-32k":       {NumRequests: 45, MaxRequestUsage: 50},
		"premiumRequests": {NumRequests: 900},
	}})
	if env == nil || env.Attributes["used_pct"] != "90.00" || env.Attributes["allowance_of"] != "gpt-4-32k" {
		t.Fatalf("reading %+v, want gpt-4-32k at 90%%", env)
	}
	if env := requestsEnvelope(at, "u", &UsageResponse{Models: map[string]ModelUsage{"premiumRequests": {NumRequests: 3}}}); env != nil {
		t.Errorf("an answer with no allowance was read as %v", env.Attributes)
	}
}

func (b *captureBus) PublishWait(_ context.Context, env *eventschema.Envelope) error {
	b.mu.Lock()
	if b.refuse > 0 {
		b.refuse--
		b.mu.Unlock()
		return errors.New("the store is busy")
	}
	b.mu.Unlock()
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
