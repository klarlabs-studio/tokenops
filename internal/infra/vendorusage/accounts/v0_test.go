package accounts

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
)

func v0Server(t *testing.T, billing, rate string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer v0k" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/v1/user/billing":
			_, _ = w.Write([]byte(billing))
		case "/v1/rate-limits":
			_, _ = w.Write([]byte(rate))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestV0ReadsBillingAndRateLimit(t *testing.T) {
	var f struct {
		Billing    json.RawMessage `json:"billing"`
		RateLimits json.RawMessage `json:"rateLimits"`
	}
	if err := json.Unmarshal([]byte(fixture(t, "v0")), &f); err != nil {
		t.Fatal(err)
	}
	srv := v0Server(t, string(f.Billing), string(f.RateLimits))
	got, err := V0{BaseURL: srv.URL}.Read(context.Background(), "v0k")
	if err != nil || !got.Subscription || len(got.Windows) != 2 || got.HasBalance {
		t.Fatalf("got %+v, %v", got, err)
	}
	if w := got.Windows[0]; w.Name != "billing cycle" || !approx(w.UsedPct, 25) || !w.ResetsAt.Equal(time.UnixMilli(1793491200000)) {
		t.Errorf("billing %+v", w)
	}
	if w := got.Windows[1]; w.Name != "requests" || !approx(w.UsedPct, 25) || !w.ResetsAt.Equal(time.Unix(1791460800, 0)) {
		t.Errorf("rate limit %+v", w)
	}
}

func TestV0LegacyAndUnknown(t *testing.T) {
	// A legacy allowance with no reported remainder has no percentage.
	srv := v0Server(t, `{"billingType":"legacy","data":{"limit":100}}`, `{"limit":10,"remaining":0}`)
	got, err := V0{BaseURL: srv.URL}.Read(context.Background(), "v0k")
	if err != nil || len(got.Windows) != 1 || got.Windows[0].Name != "requests" || got.Windows[0].UsedPct != 100 {
		t.Errorf("legacy = %+v, %v", got, err)
	}
	unknown := v0Server(t, `{"billingType":"enterprise"}`, `{}`)
	if _, err := (V0{BaseURL: unknown.URL}).Read(context.Background(), "v0k"); err == nil || errors.Is(err, usage.ErrAuth) {
		t.Errorf("an unknown billing type = %v", err)
	}
	if _, err := (V0{BaseURL: srv.URL}).Read(context.Background(), "bad"); !errors.Is(err, usage.ErrAuth) {
		t.Errorf("a refused key = %v, want ErrAuth", err)
	}
}
