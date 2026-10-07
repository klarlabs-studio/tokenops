package claudecodeoauth

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/claudeusagemeter"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

var now = time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)

// A fake credential: no real token appears in these tests.
func credsJSON(token string, expires time.Time, scopes string) []byte {
	return []byte(`{"claudeAiOauth":{"accessToken":"` + token + `","refreshToken":"never-read","expiresAt":` +
		strconv.FormatInt(expires.UnixMilli(), 10) + `,"scopes":` + scopes + `,"subscriptionType":"max"}}`)
}

func TestParse(t *testing.T) {
	c, err := Parse(credsJSON("tok", now.Add(time.Hour), `["user:inference","user:profile"]`))
	if err != nil || c.AccessToken != "tok" || !c.ExpiresAt.Equal(now.Add(time.Hour)) || c.SubscriptionType != "max" {
		t.Fatalf("parse = %+v, %v", c, err)
	}
	if !c.Expired(now.Add(59*time.Minute+30*time.Second)) || c.Expired(now) {
		t.Error("expiry margin")
	}
	if _, err := Parse(credsJSON("tok", now, `["user:inference"]`)); !errors.Is(err, ErrNoUsageScope) {
		t.Errorf("setup-token scope: %v", err)
	}
	if _, err := Parse([]byte(`{"mcpOAuth":{}}`)); !errors.Is(err, ErrNotSignedIn) {
		t.Errorf("MCP-only item: %v", err)
	}
}

const usageBody = `{"five_hour":{"utilization":10,"resets_at":"2026-10-06T15:00:00Z"},
 "seven_day":{"utilization":22,"resets_at":"2026-10-09T23:00:00Z"}}`

// fakeClient stands in for the HTTP client.
type fakeClient func(token string, now time.Time) (*claudeusagemeter.UsageResponse, error)

func (f fakeClient) Usage(_ context.Context, token string, now time.Time) (*claudeusagemeter.UsageResponse, error) {
	return f(token, now)
}

func serves(body string) fakeClient {
	return func(string, time.Time) (*claudeusagemeter.UsageResponse, error) {
		return claudeusagemeter.ParseUsage([]byte(body))
	}
}

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

// promptingStore is a store that asks the operator, as the Keychain does.
type promptingStore struct{ *fakeStore }

func (promptingStore) Prompts() bool { return true }

type fakeStore struct {
	reads int
	creds []Credentials
	err   error
}

func (*fakeStore) Name() string { return "fake" }
func (f *fakeStore) Read(context.Context) (Credentials, error) {
	f.reads++
	if f.err != nil {
		return Credentials{}, f.err
	}
	c := f.creds[min(f.reads, len(f.creds))-1]
	return c, nil
}

// A reading is published under this source's tag in the meter's shape,
// once per change; the token is read once and kept while it is valid.
func TestPollerPublishes(t *testing.T) {
	bus := &recordingBus{}
	store := &fakeStore{creds: []Credentials{{AccessToken: "tok", ExpiresAt: now.Add(time.Hour)}}}
	p := NewPoller(bus, PollerOptions{Stores: []Store{store}, Client: serves(usageBody), Now: func() time.Time { return now }})
	p.Scan(context.Background())
	p.Scan(context.Background())
	if len(bus.got) != 1 || bus.got[0].Source != SourceTag || bus.got[0].Attributes["seven_day_used_pct"] != "22.00" {
		t.Fatalf("published %+v", bus.got)
	}
	if _, ok := bus.got[0].Attributes["org_id"]; ok {
		t.Error("an org id nobody resolved")
	}
	if store.reads != 1 {
		t.Errorf("token read %d times", store.reads)
	}
}

// Refused, the poller re-reads once: Claude Code may have renewed the
// token since. It never refreshes it itself.
func TestPollerRereadsARenewedToken(t *testing.T) {
	client := fakeClient(func(token string, _ time.Time) (*claudeusagemeter.UsageResponse, error) {
		if token != "renewed" {
			return nil, ErrExpired
		}
		return claudeusagemeter.ParseUsage([]byte(usageBody))
	})
	bus := &recordingBus{}
	store := &fakeStore{creds: []Credentials{{AccessToken: "old"}, {AccessToken: "renewed"}}}
	p := NewPoller(bus, PollerOptions{Stores: []Store{store}, Client: client, Now: func() time.Time { return now }})
	p.Scan(context.Background())
	if len(bus.got) != 1 || store.reads != 2 {
		t.Errorf("published %d after %d reads", len(bus.got), store.reads)
	}
}

// A declined Keychain is not asked again for hours, and a 429 holds every
// request until the time it names.
func TestPollerBacksOff(t *testing.T) {
	at := now
	// A denied Keychain read, as the Keychain store reports it.
	counting := promptingStore{&fakeStore{err: ErrKeychainDenied}}
	p := NewPoller(nil, PollerOptions{Stores: []Store{counting}, Now: func() time.Time { return at }})
	p.Scan(context.Background())
	at = at.Add(time.Hour)
	p.Scan(context.Background())
	if counting.reads != 1 {
		t.Errorf("keychain asked %d times within the backoff", counting.reads)
	}
	at = at.Add(keychainBackoff)
	p.Scan(context.Background())
	if counting.reads != 2 {
		t.Errorf("keychain not asked again after the backoff: %d", counting.reads)
	}

	calls := 0
	limited := fakeClient(func(_ string, now time.Time) (*claudeusagemeter.UsageResponse, error) {
		calls++
		return nil, &RateLimitedError{Until: now.Add(600 * time.Second)}
	})
	at = now
	store := &fakeStore{creds: []Credentials{{AccessToken: "tok"}}}
	p = NewPoller(nil, PollerOptions{Stores: []Store{store}, Client: limited, Now: func() time.Time { return at }})
	p.Scan(context.Background())
	at = at.Add(5 * time.Minute)
	p.Scan(context.Background())
	at = at.Add(6 * time.Minute)
	p.Scan(context.Background())
	if calls != 2 {
		t.Errorf("endpoint called %d times; the 429 should hold the second", calls)
	}
}
