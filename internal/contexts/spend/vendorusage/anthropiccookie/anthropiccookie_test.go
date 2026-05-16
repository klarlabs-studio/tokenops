package anthropiccookie

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/felixgeelhaar/tokenops/pkg/eventschema"
)

const sampleOrgs = `[{"uuid":"org-abc","name":"My Org","capabilities":["claude_pro"]}]`

const sampleUsage = `{
  "five_hour":     {"utilization_pct": 42.5, "reset_at": "2026-05-16T13:00:00Z"},
  "seven_day":     {"utilization_pct": 71.0},
  "seven_day_opus":{"utilization_pct": 83.2},
  "extra_usage":   {"current_spending": 4.20, "budget_limit": 10.00}
}`

// Client.Organizations sends the sessionKey cookie + browser UA and
// decodes the array shape.
func TestClientOrganizationsHappyPath(t *testing.T) {
	var gotCookie, gotUA, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotCookie = r.Header.Get("Cookie")
		gotUA = r.Header.Get("User-Agent")
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(sampleOrgs))
	}))
	defer srv.Close()
	c := NewClient("sk")
	c.BaseURL = srv.URL
	orgs, err := c.Organizations(context.Background())
	if err != nil {
		t.Fatalf("Organizations: %v", err)
	}
	if gotCookie != "sessionKey=sk" {
		t.Errorf("cookie = %q", gotCookie)
	}
	if gotUA == "" {
		t.Errorf("user-agent must be set to avoid Cloudflare 403")
	}
	if gotPath != "/api/organizations" {
		t.Errorf("path = %q", gotPath)
	}
	if len(orgs) != 1 || orgs[0].UUID != "org-abc" {
		t.Errorf("orgs = %+v", orgs)
	}
}

// Empty cookie short-circuits with ErrMissingCookie before HTTP.
func TestClientMissingCookie(t *testing.T) {
	if _, err := (&Client{}).Organizations(context.Background()); err != ErrMissingCookie {
		t.Errorf("want ErrMissingCookie; got %v", err)
	}
	if _, err := (&Client{}).Usage(context.Background(), "x"); err != ErrMissingCookie {
		t.Errorf("want ErrMissingCookie; got %v", err)
	}
}

// 401 maps to ErrUnauthorized so the poller can log the specific
// "re-paste cookie" hint instead of generic http noise.
func TestClient401MapsToErrUnauthorized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(401)
	}))
	defer srv.Close()
	c := NewClient("expired")
	c.BaseURL = srv.URL
	if _, err := c.Organizations(context.Background()); err != ErrUnauthorized {
		t.Errorf("want ErrUnauthorized; got %v", err)
	}
}

// Usage shape includes all three windows + extra_usage.
func TestClientUsageHappyPath(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(sampleUsage))
	}))
	defer srv.Close()
	c := NewClient("sk")
	c.BaseURL = srv.URL
	u, err := c.Usage(context.Background(), "org-abc")
	if err != nil {
		t.Fatalf("Usage: %v", err)
	}
	if gotPath != "/api/organizations/org-abc/usage" {
		t.Errorf("path = %q", gotPath)
	}
	if u.FiveHour.UtilizationPct != 42.5 {
		t.Errorf("five_hour = %v", u.FiveHour.UtilizationPct)
	}
	if u.SevenDay.UtilizationPct != 71.0 {
		t.Errorf("seven_day = %v", u.SevenDay.UtilizationPct)
	}
	if u.SevenDayOpus.UtilizationPct != 83.2 {
		t.Errorf("seven_day_opus = %v", u.SevenDayOpus.UtilizationPct)
	}
	if u.ExtraUsage == nil || u.ExtraUsage.CurrentSpending != 4.20 {
		t.Errorf("extra_usage = %+v", u.ExtraUsage)
	}
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

// Poller resolves org_id via /api/organizations on first scan, emits
// one envelope, and dedupes against unchanged five_hour reset_at on
// subsequent scans.
func TestPollerResolvesOrgAndEmitsOnceUntilResetChanges(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/organizations":
			_, _ = w.Write([]byte(sampleOrgs))
		case "/api/organizations/org-abc/usage":
			_, _ = w.Write([]byte(sampleUsage))
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	bus := &captureBus{}
	p := NewPoller(bus, PollerOptions{
		SessionKey: "sk",
		Interval:   time.Hour,
		BaseURL:    srv.URL,
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err := p.ensureClient(); err != nil {
		t.Fatal(err)
	}
	p.scan(context.Background())
	if got := bus.PublishedCount(); got != 1 {
		t.Fatalf("want 1 envelope; got %d", got)
	}
	p.scan(context.Background())
	if got := bus.PublishedCount(); got != 1 {
		t.Errorf("same reset_at must dedupe; total %d", got)
	}
}

// newEnvelope serialises all three utilization buckets into Attributes
// so a future Max-aware session_budget can consume them.
func TestNewEnvelopeShape(t *testing.T) {
	usage := &UsageResponse{
		FiveHour:     Window{UtilizationPct: 12.5, ResetAt: "2026-05-16T13:00:00Z"},
		SevenDay:     Window{UtilizationPct: 33.0},
		SevenDayOpus: Window{UtilizationPct: 55.5},
		ExtraUsage:   &ExtraUsage{CurrentSpending: 1.50, BudgetLimit: 10.00},
	}
	env := newEnvelope(time.Now().UTC(), "org-abc", usage)
	if env.Source != SourceTag {
		t.Errorf("source = %q", env.Source)
	}
	if env.Attributes["five_hour_used_pct"] != "12.50" {
		t.Errorf("five_hour_used_pct = %q", env.Attributes["five_hour_used_pct"])
	}
	if env.Attributes["seven_day_used_pct"] != "33.00" {
		t.Errorf("seven_day_used_pct = %q", env.Attributes["seven_day_used_pct"])
	}
	if env.Attributes["seven_day_opus_used_pct"] != "55.50" {
		t.Errorf("seven_day_opus_used_pct = %q", env.Attributes["seven_day_opus_used_pct"])
	}
	if env.Attributes["extra_usage_current"] != "1.50" {
		t.Errorf("extra_usage_current = %q", env.Attributes["extra_usage_current"])
	}
}
