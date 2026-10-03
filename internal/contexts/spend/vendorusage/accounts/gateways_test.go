package accounts

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

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

func TestLiteLLMKeyInfo(t *testing.T) {
	var f fakeGateway
	srv := f.server(t, "/health/liveliness", `"I'm alive!"`, "/key/info", "Authorization", "Bearer ",
		`{"key":"x","info":{"spend":12.5,"max_budget":50,"budget_duration":"30d","budget_reset_at":"2026-11-01T00:00:00Z","status":"active"}}`)
	ctx := context.Background()
	if !(LiteLLM{}).Recognise(ctx, nil, srv.URL) || (Bifrost{}).Recognise(ctx, nil, srv.URL) {
		t.Fatal("recognition")
	}
	got, err := LiteLLM{}.Read(ctx, nil, srv.URL, "vk")
	if err != nil || got.UsedUSD != 12.5 || got.LimitUSD != 50 || len(got.Windows) != 1 || got.Windows[0].UsedPct != 25 || got.Windows[0].ResetsAt.IsZero() {
		t.Fatalf("got %+v, %v", got, err)
	}
}

func TestBifrostQuota(t *testing.T) {
	var f fakeGateway
	srv := f.server(t, "/health", `{"status":"ok","components":{"db_pings":"ok"}}`, "/api/governance/virtual-keys/quota", "x-bf-vk", "",
		`{"virtual_key_name":"me","is_active":true,"budgets":[
		 {"max_limit":10,"current_usage":2,"reset_duration":"1d","last_reset":"2026-10-03T00:00:00Z"},
		 {"max_limit":100,"current_usage":40,"reset_duration":"1M","last_reset":"2026-10-01T00:00:00Z"}]}`)
	ctx := context.Background()
	if !(Bifrost{}).Recognise(ctx, nil, srv.URL) {
		t.Fatal("not recognised")
	}
	got, err := Bifrost{}.Read(ctx, nil, srv.URL, "vk")
	if err != nil || len(got.Windows) != 2 || got.UsedUSD != 40 || got.LimitUSD != 100 || got.LimitReached {
		t.Fatalf("got %+v, %v", got, err)
	}
	if !got.Windows[0].ResetsAt.Equal(time.Date(2026, 10, 4, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("daily reset %v", got.Windows[0].ResetsAt)
	}
}

func TestClawRouterUsageInMicros(t *testing.T) {
	var f fakeGateway
	srv := f.server(t, "/v1/health", `{"ok":true,"service":"clawrouter-edge"}`, "/v1/usage", "Authorization", "Bearer ",
		`{"policyId":"p","budget":{"configured":true,"ledger":"durable_object","limitMicros":50000000,"spentMicros":12500000}}`)
	ctx := context.Background()
	if !(ClawRouter{}).Recognise(ctx, nil, srv.URL) {
		t.Fatal("not recognised")
	}
	got, err := ClawRouter{}.Read(ctx, nil, srv.URL, "vk")
	if err != nil || got.UsedUSD != 12.5 || got.LimitUSD != 50 {
		t.Fatalf("got %+v, %v", got, err)
	}
}

// The key goes to a recognised gateway at the address the harness uses,
// never to a host nothing recognised, and recognition asks without it.
func TestPollerSendsAGatewayKeyOnlyWhereItBelongs(t *testing.T) {
	var lite, unknown fakeGateway
	liteSrv := lite.server(t, "/health/liveliness", `"I'm alive!"`, "/key/info", "Authorization", "Bearer ",
		`{"info":{"spend":3,"max_budget":10}}`)
	otherSrv := unknown.server(t, "/nothing", "", "/key/info", "Authorization", "Bearer ", `{}`)
	p := NewPoller(nil, PollerOptions{Readers: []Reader{}, Credentials: func() []Credential {
		return []Credential{
			{Endpoint: GatewayEndpoint, BaseURL: liteSrv.URL + "/anthropic", Key: "vk"},
			{Endpoint: GatewayEndpoint, BaseURL: otherSrv.URL + "/v1", Key: "secret"},
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
