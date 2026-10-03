package accounts

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

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

func TestOpenRouter(t *testing.T) {
	ctx := context.Background()
	for name, tc := range map[string]struct {
		body string
		want Reading
	}{
		"no cap": {`{"data":{"limit":null,"limit_reset":null,"limit_remaining":null,"usage":90,"usage_monthly":12.5}}`,
			Reading{Scope: "key", UsedUSD: 12.5, HasUsed: true}},
		"monthly cap": {`{"data":{"limit":50,"limit_reset":"monthly","limit_remaining":37.5,"usage":90,"usage_monthly":12.5}}`,
			Reading{Scope: "key", UsedUSD: 12.5, HasUsed: true, LimitUSD: 50}},
		"lifetime cap spent": {`{"data":{"limit":100,"limit_reset":null,"limit_remaining":0,"usage":100,"usage_monthly":3}}`,
			Reading{Scope: "key", UsedUSD: 100, HasUsed: true, LimitUSD: 100, LimitReached: true}},
	} {
		srv := serve(t, "/api/v1/key", "sk-or", tc.body)
		got, err := OpenRouter{BaseURL: srv.URL}.Read(ctx, "sk-or")
		srv.Close()
		if err != nil || !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: %+v %v, want %+v", name, got, err, tc.want)
		}
	}
}

func TestDeepSeekUsesTheUSDBalance(t *testing.T) {
	srv := serve(t, "/user/balance", "sk-ds", `{"is_available":true,"balance_infos":[{"currency":"CNY","total_balance":"100.00"},{"currency":"USD","total_balance":"7.25"}]}`)
	defer srv.Close()
	got, err := DeepSeek{BaseURL: srv.URL}.Read(context.Background(), "sk-ds")
	if err != nil || !got.HasBalance || got.BalanceUSD != 7.25 || got.LimitReached || got.HasUsed {
		t.Errorf("%+v %v", got, err)
	}
}

func TestMoonshotEmptyBalanceIsReached(t *testing.T) {
	srv := serve(t, "/v1/users/me/balance", "sk-ms", `{"code":0,"data":{"available_balance":0,"voucher_balance":0,"cash_balance":0},"status":true}`)
	defer srv.Close()
	got, err := Moonshot{BaseURL: srv.URL}.Read(context.Background(), "sk-ms")
	if err != nil || !got.HasBalance || !got.LimitReached {
		t.Errorf("%+v %v", got, err)
	}
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
	p := NewPoller(bus, PollerOptions{
		Readers: []Reader{OpenRouter{BaseURL: srv.URL}, DeepSeek{BaseURL: ds.URL}},
		Credentials: func() []Credential {
			return []Credential{
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
	if _, err := (OpenRouter{BaseURL: srv.URL}).Read(context.Background(), "sk-bad"); !errors.Is(err, ErrAuth) {
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
