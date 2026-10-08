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
	return v0ServerFor(t, billing, rate, nil)
}

// v0ServerFor is v0Server recording each request's scope.
func v0ServerFor(t *testing.T, billing, rate string, scopes *[]string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if scopes != nil {
			*scopes = append(*scopes, r.URL.Path+"|"+r.URL.RawQuery)
		}
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

// A scope reads that project: both requests carry it as ?scope=, as
// CodexBar sends it, and the default scope sends none.
func TestV0ReadsAProjectScope(t *testing.T) {
	var asked []string
	srv := v0ServerFor(t, `{"billingType":"legacy","data":{"limit":100,"remaining":40}}`, `{"limit":10,"remaining":5}`, &asked)
	scoped := usage.WithScopes([]usage.Reader{V0{BaseURL: srv.URL}}, map[string]string{"v0": "my project&x=1"})[0]
	got, err := scoped.Read(context.Background(), "v0k")
	if err != nil || got.Scope != "project" || len(got.Windows) != 2 {
		t.Fatalf("project = %+v, %v", got, err)
	}
	want := []string{"/v1/user/billing|scope=my+project%26x%3D1", "/v1/rate-limits|scope=my+project%26x%3D1"}
	if len(asked) != 2 || asked[0] != want[0] || asked[1] != want[1] {
		t.Errorf("asked %q, want %q", asked, want)
	}
	asked = nil
	if got, err := (V0{BaseURL: srv.URL}).Read(context.Background(), "v0k"); err != nil || got.Scope != "account" || asked[0] != "/v1/user/billing|" {
		t.Errorf("default = %+v %v %q", got, err, asked)
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
