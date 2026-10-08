package accounts

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

func serve(t *testing.T, path, key, body string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+key {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.URL.Path != path {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(body))
	}))
}

// serveRaw is serve for z.ai, which takes the key without "Bearer".
func serveRaw(t *testing.T, path, key, body string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != key || r.URL.Path != path {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(body))
	}))
}

func approx(a, b float64) bool { return a-b < 0.001 && b-a < 0.001 }

// fixture is a provider's recorded answer, testdata/<id>.json: the
// vendor's documented example or its own client's fixture, not a live
// answer.
func fixture(t *testing.T, id string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", id+".json"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// The poller hands each reader only its own vendor's keys, tries them in
// order, and does not call a vendor with no key.
func TestPollerTriesOnlyMatchingKeys(t *testing.T) {
	srv := serve(t, "/api/v1/key", "sk-good", `{"data":{"limit":null,"usage_monthly":4}}`)
	defer srv.Close()
	deepseekCalled := false
	ds := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { deepseekCalled = true }))
	defer ds.Close()
	bus := &captureBus{}
	p := usage.NewPoller(bus, usage.PollerOptions{
		Readers: []usage.Reader{OpenRouter{BaseURL: srv.URL}, DeepSeek{BaseURL: ds.URL}},
		Credentials: func() []usage.Credential {
			return []usage.Credential{
				{Endpoint: "openrouter", Key: "sk-stale"},
				{Endpoint: "fireworks", Key: "sk-good"}, // another vendor's key is never sent
				{Endpoint: "openrouter", Key: "sk-good"},
			}
		},
		Now: func() time.Time { return time.Date(2026, 10, 3, 9, 0, 0, 0, time.UTC) },
	})
	p.Scan(context.Background())
	if deepseekCalled {
		t.Error("DeepSeek was called with no DeepSeek key")
	}
	if len(bus.got) != 1 || bus.got[0].Source != "openrouter-account" || bus.got[0].Attributes["extra_usage_used"] != "4.00" {
		t.Fatalf("published %+v", bus.got)
	}
	// An unchanged reading is not stored again.
	p.Scan(context.Background())
	if len(bus.got) != 1 {
		t.Errorf("published %d", len(bus.got))
	}
}

func TestRefusedKeyIsReported(t *testing.T) {
	srv := serve(t, "/api/v1/key", "sk-good", `{}`)
	defer srv.Close()
	if _, err := (OpenRouter{BaseURL: srv.URL}).Read(context.Background(), "sk-bad"); !errors.Is(err, usage.ErrAuth) {
		t.Errorf("err %v", err)
	}
}

type captureBus struct{ got []*eventschema.Envelope }

func (b *captureBus) Publish(env *eventschema.Envelope) { b.got = append(b.got, env) }
func (b *captureBus) PublishWait(_ context.Context, env *eventschema.Envelope) error {
	b.got = append(b.got, env)
	return nil
}
func (b *captureBus) DroppedCount() int64       { return 0 }
func (b *captureBus) PublishedCount() int64     { return int64(len(b.got)) }
func (b *captureBus) Close(time.Duration) error { return nil }

// A subscription is stored with its windows and without the pay-as-you-go
// attributes, so headroom does not bind it as per-token billing.
func TestSubscriptionEnvelope(t *testing.T) {
	at := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	env := usage.NewEnvelope(at, Kimi{}, usage.Reading{Scope: "account", Subscription: true, Windows: []usage.Window{
		{Name: "5h", UsedPct: 30, Duration: 5 * time.Hour, ResetsAt: at.Add(time.Hour)},
	}})
	a := env.Attributes
	if a["billing"] != "subscription" || a["window_0_name"] != "5h" || a["window_0_used_pct"] != "30.00" ||
		a["window_0_duration_min"] != "300" || a["window_0_reset_at"] != "2026-10-03T13:00:00Z" {
		t.Errorf("attributes %v", a)
	}
	if _, ok := a["extra_usage_limit"]; ok {
		t.Error("a subscription carries pay-as-you-go attributes")
	}
}

// fakeGateway serves a health route without a key and a budget route that
// needs it, and records every key it was sent.
type fakeGateway struct {
	mu   sync.Mutex
	keys []string
}

func (f *fakeGateway) server(t *testing.T, health, healthBody, budget, header, prefix, budgetBody string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if v := r.Header.Get(header); v != "" {
			f.mu.Lock()
			f.keys = append(f.keys, v)
			f.mu.Unlock()
		}
		switch r.URL.Path {
		case health:
			_, _ = w.Write([]byte(healthBody))
		case budget:
			if r.Header.Get(header) != prefix+"vk" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			_, _ = w.Write([]byte(budgetBody))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// The key goes to a recognised gateway at the address the harness uses,
// never to a host nothing recognised, and recognition asks without it.
func TestPollerSendsAGatewayKeyOnlyWhereItBelongs(t *testing.T) {
	var lite, unknown fakeGateway
	liteSrv := lite.server(t, "/health/liveliness", `"I'm alive!"`, "/key/info", "Authorization", "Bearer ",
		`{"info":{"spend":3,"max_budget":10}}`)
	otherSrv := unknown.server(t, "/nothing", "", "/key/info", "Authorization", "Bearer ", `{}`)
	p := usage.NewPoller(nil, usage.PollerOptions{Readers: []usage.Reader{}, Gateways: Gateways(), Credentials: func() []usage.Credential {
		return []usage.Credential{
			{Endpoint: usage.GatewayEndpoint, BaseURL: liteSrv.URL + "/anthropic", Key: "vk"},
			{Endpoint: usage.GatewayEndpoint, BaseURL: otherSrv.URL + "/v1", Key: "secret"},
		}
	}})
	p.Scan(context.Background())
	if len(lite.keys) != 1 || lite.keys[0] != "Bearer vk" {
		t.Errorf("recognised gateway got keys %v, want one read", lite.keys)
	}
	if len(unknown.keys) != 0 {
		t.Errorf("an unrecognised host was sent a key: %v", unknown.keys)
	}
	// Recognition is cached: a second scan reads but does not re-probe.
	p.Scan(context.Background())
	if len(lite.keys) != 2 {
		t.Errorf("second scan keys %v", lite.keys)
	}
	if strings.Contains(strings.Join(unknown.keys, ""), "secret") {
		t.Error("the key leaked")
	}
}
