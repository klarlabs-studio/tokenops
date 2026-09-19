package claudeusagemeter

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/pkg/eventschema"
)

const sampleOrgs = `[{"uuid":"org-abc","name":"My Org","capabilities":["claude_pro"]}]`

// enterpriseUsage is a real Claude Enterprise response (2026-09-19), cut to
// the blocks this meter reads plus neighbours it must ignore: every window
// null, spend in extra_usage.
const enterpriseUsage = `{
  "five_hour": null, "seven_day": null, "seven_day_opus": null,
  "seven_day_sonnet": null, "limits": null,
  "extra_usage": {"is_enabled": true, "monthly_limit": 150000, "used_credits": 109563,
    "utilization": 73.042, "currency": "USD", "decimal_places": 2, "disabled_reason": null,
    "user_disabled": false, "spend_limit_reached": false, "credits_ever_enabled": true,
    "daily": null, "weekly": null}
}`

// chatOnlyUsage is a personal chat-only org on the same account: nothing to
// meter at all.
const chatOnlyUsage = `{"five_hour": null, "seven_day": null, "seven_day_opus": null, "extra_usage": null}`

// windowedUsage is a real Claude Max response (2026-09-19): windows carry
// utilization and resets_at alongside dollar fields that are null on a
// subscription, and extra_usage is present but disabled.
const windowedUsage = `{
  "five_hour": {"utilization": 18, "resets_at": "2026-09-19T20:00:00.048431+00:00",
    "limit_dollars": null, "used_dollars": null, "remaining_dollars": null, "locked_reason": null},
  "seven_day": {"utilization": 36, "resets_at": "2026-09-25T23:00:00.048451+00:00",
    "limit_dollars": null, "used_dollars": null, "remaining_dollars": null, "locked_reason": null},
  "seven_day_opus": null, "seven_day_sonnet": null,
  "extra_usage": {"is_enabled": false, "monthly_limit": null, "used_credits": null, "utilization": null,
    "currency": null, "decimal_places": null, "disabled_reason": null, "user_disabled": false,
    "spend_limit_reached": false, "credits_ever_enabled": false, "daily": null, "weekly": null}
}`

// inventedUsage is the shape this meter was originally written against,
// which no real response has ever had. It must be refused, not zeroed.
const inventedUsage = `{
  "five_hour":     {"utilization_pct": 42.5, "reset_at": "2026-05-16T13:00:00Z"},
  "seven_day":     {"utilization_pct": 71.0},
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

func usageFrom(t *testing.T, body string) *UsageResponse {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()
	c := NewClient("sk")
	c.BaseURL = srv.URL
	u, err := c.Usage(context.Background(), "org-abc")
	if err != nil {
		t.Fatalf("Usage: %v", err)
	}
	return u
}

// Enterprise reports spend, not windows. The amounts are minor units.
func TestUsageDecodesEnterpriseSpend(t *testing.T) {
	u := usageFrom(t, enterpriseUsage)
	if u.FiveHour != nil || u.SevenDay != nil || u.SevenDayOpus != nil {
		t.Errorf("null windows decoded as present: %+v %+v %+v", u.FiveHour, u.SevenDay, u.SevenDayOpus)
	}
	if u.ExtraUsage == nil {
		t.Fatal("extra_usage missing")
	}
	used, limit := u.ExtraUsage.Amounts()
	if used != 1095.63 || limit != 1500 {
		t.Errorf("amounts = %.2f of %.2f, want 1095.63 of 1500.00 (minor units, 2 places)", used, limit)
	}
	if u.ExtraUsage.Currency != "USD" || len(u.Unrecognised) != 0 {
		t.Errorf("currency %q, unrecognised %v", u.ExtraUsage.Currency, u.Unrecognised)
	}
}

func TestUsageWithNothingToMeterHasNoSignal(t *testing.T) {
	if u := usageFrom(t, chatOnlyUsage); u.HasSignal() {
		t.Errorf("chat-only org reported as meterable: %+v", u)
	}
}

// On Max, the windows are the reading and a disabled extra_usage is simply
// nothing to meter — not a block it failed to read.
func TestUsageDecodesMaxWindows(t *testing.T) {
	u := usageFrom(t, windowedUsage)
	if u.FiveHour == nil || *u.FiveHour.Utilization != 18 || u.FiveHour.ResetsAt != "2026-09-19T20:00:00.048431+00:00" {
		t.Errorf("five_hour = %+v", u.FiveHour)
	}
	if u.SevenDay == nil || *u.SevenDay.Utilization != 36 {
		t.Errorf("seven_day = %+v", u.SevenDay)
	}
	if u.SevenDayOpus != nil {
		t.Errorf("null seven_day_opus decoded as present")
	}
	if u.ExtraUsage != nil || len(u.Unrecognised) != 0 {
		t.Errorf("disabled extra_usage: kept=%v unrecognised=%v, want dropped quietly", u.ExtraUsage, u.Unrecognised)
	}
}

// A block without the fields this meter reads is dropped and named — never
// turned into a 0% that headroom would trust as the vendor's own figure.
func TestUsageRefusesAShapeItCannotRead(t *testing.T) {
	u := usageFrom(t, inventedUsage)
	if u.HasSignal() {
		t.Errorf("unreadable blocks survived as readings: %+v", u)
	}
	for _, want := range []string{"five_hour", "seven_day", "extra_usage"} {
		if !slices.Contains(u.Unrecognised, want) {
			t.Errorf("unrecognised = %v, missing %s", u.Unrecognised, want)
		}
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

// meterServer serves orgs and a per-org usage body that the test can
// change between scans.
type meterServer struct {
	mu    sync.Mutex
	orgs  string
	usage map[string]string
}

func (m *meterServer) set(org, body string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.usage[org] = body
}

func (m *meterServer) start(t *testing.T) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m.mu.Lock()
		defer m.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/organizations" {
			_, _ = w.Write([]byte(m.orgs))
			return
		}
		for org, body := range m.usage {
			if r.URL.Path == "/api/organizations/"+org+"/usage" {
				_, _ = w.Write([]byte(body))
				return
			}
		}
		w.WriteHeader(404)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func newTestPoller(t *testing.T, srv *httptest.Server, bus *captureBus, logs io.Writer) *Poller {
	t.Helper()
	p := NewPoller(bus, PollerOptions{
		SessionKey: "sk",
		Interval:   time.Hour,
		BaseURL:    srv.URL,
		Logger:     slog.New(slog.NewTextHandler(logs, nil)),
	})
	if err := p.ensureClient(); err != nil {
		t.Fatal(err)
	}
	return p
}

// A reading is stored whenever it changes. The meter used to derive the
// event ID from the five-hour reset, and the store keeps the first event
// per ID, so only the first poll after each reset survived — and on
// Enterprise, which has no window, the first reading ever.
func TestPollerStoresEachChangedReading(t *testing.T) {
	m := &meterServer{orgs: sampleOrgs, usage: map[string]string{"org-abc": enterpriseUsage}}
	bus := &captureBus{}
	p := newTestPoller(t, m.start(t), bus, io.Discard)

	p.scan(context.Background())
	p.scan(context.Background())
	if got := bus.PublishedCount(); got != 1 {
		t.Fatalf("an unchanged reading was stored %d times, want 1", got)
	}
	m.set("org-abc", strings.Replace(enterpriseUsage, `"used_credits": 109563`, `"used_credits": 112000`, 1))
	p.scan(context.Background())
	if got := bus.PublishedCount(); got != 2 {
		t.Fatalf("a changed reading was not stored: %d events, want 2", got)
	}
	if bus.envelopes[0].ID == bus.envelopes[1].ID {
		t.Error("two readings share an ID; the store would keep only the first")
	}
	if got := bus.envelopes[1].Attributes["extra_usage_used"]; got != "1120.00" {
		t.Errorf("latest extra_usage_used = %q, want 1120.00", got)
	}
}

// An account can hold a personal chat-only org alongside the Enterprise
// one. The meter picks the one with something to meter, not whichever the
// API lists first.
func TestPollerPicksTheOrgThatReportsUsage(t *testing.T) {
	m := &meterServer{
		orgs:  `[{"uuid":"personal","name":"Personal"},{"uuid":"work","name":"Work"}]`,
		usage: map[string]string{"personal": chatOnlyUsage, "work": enterpriseUsage},
	}
	bus := &captureBus{}
	p := newTestPoller(t, m.start(t), bus, io.Discard)
	p.scan(context.Background())
	if p.orgID != "work" {
		t.Errorf("metered org %q, want the one reporting usage", p.orgID)
	}
	if bus.PublishedCount() != 1 {
		t.Errorf("published %d, want 1", bus.PublishedCount())
	}
}

// Nothing to meter publishes nothing, and an unreadable shape says so.
func TestPollerStoresNoReadingItCannotBack(t *testing.T) {
	m := &meterServer{orgs: sampleOrgs, usage: map[string]string{"org-abc": inventedUsage}}
	bus := &captureBus{}
	var logs strings.Builder
	p := newTestPoller(t, m.start(t), bus, &logs)
	p.scan(context.Background())
	if bus.PublishedCount() != 0 {
		t.Errorf("stored %d readings from a response it could not read", bus.PublishedCount())
	}
	if !strings.Contains(logs.String(), "cannot read") {
		t.Errorf("unreadable response not reported:\n%s", logs.String())
	}
}

// Only reported blocks become attributes: a window Anthropic did not return
// must not reach headroom as 0%.
func TestNewEnvelopeWritesOnlyReportedBlocks(t *testing.T) {
	var u UsageResponse
	if err := json.Unmarshal([]byte(enterpriseUsage), &u); err != nil {
		t.Fatal(err)
	}
	u.dropUnreadable()
	env := newEnvelope(time.Now().UTC(), "org-abc", &u)
	for _, k := range []string{"five_hour_used_pct", "seven_day_used_pct", "seven_day_opus_used_pct"} {
		if v, ok := env.Attributes[k]; ok {
			t.Errorf("%s = %q written for a window Anthropic did not report", k, v)
		}
	}
	want := map[string]string{
		"extra_usage_used": "1095.63", "extra_usage_limit": "1500.00",
		"extra_usage_currency": "USD", "extra_usage_limit_reached": "false",
	}
	for k, v := range want {
		if env.Attributes[k] != v {
			t.Errorf("%s = %q, want %q", k, env.Attributes[k], v)
		}
	}
}

func (b *captureBus) PublishWait(_ context.Context, env *eventschema.Envelope) error {
	b.Publish(env)
	return nil
}
