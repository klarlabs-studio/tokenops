package accounts

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
)

func factoryNow() time.Time { return time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC) }

// factoryServer is api.factory.ai and app.factory.ai: it answers routes
// for the bearer "fk-good" or the session cookie "wos-session=ws", and
// records each request's query.
func factoryServer(t *testing.T, routes map[string]string, queries *[]string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if queries != nil {
			*queries = append(*queries, r.URL.Path+"?"+r.URL.RawQuery)
		}
		if r.Header.Get("Authorization") != "Bearer fk-good" && !strings.Contains(r.Header.Get("Cookie"), "wos-session=ws") {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.Header.Get("X-Factory-Client") != "web-app" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		body, ok := routes[r.URL.Path]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestFactoryReadsTokenRateLimits(t *testing.T) {
	srv := factoryServer(t, routesOf(t, "factory"), nil)
	got, err := Factory{API: srv.URL, App: srv.URL, Now: factoryNow}.Read(context.Background(), "fk-good")
	if err != nil || !got.Subscription || len(got.Windows) != 6 {
		t.Fatalf("got %+v, %v", got, err)
	}
	want := []struct {
		name string
		pct  float64
		in   time.Duration
	}{{"5h", 12, time.Hour}, {"week", 34, 2 * time.Hour}, {"month", 56, 3 * time.Hour},
		{"core 5h", 7, 30 * time.Minute}, {"core week", 8, 2800 * time.Second}, {"core month", 9, 3800 * time.Second}}
	for i, w := range want {
		g := got.Windows[i]
		if g.Name != w.name || g.UsedPct != w.pct || !g.ResetsAt.Equal(factoryNow().Add(w.in)) {
			t.Errorf("window %d = %+v, want %s %v", i, g, w.name, w.pct)
		}
	}
	if got.Windows[0].Duration != 5*time.Hour || got.Windows[1].Duration != 7*24*time.Hour {
		t.Errorf("durations %v %v", got.Windows[0].Duration, got.Windows[1].Duration)
	}
	if !got.HasBalance || got.BalanceUSD != 25 {
		t.Errorf("extra usage %+v", got)
	}
}

// A plan not on token rate limits reads its Standard and Premium token
// allowances for the period, for the signed-in user.
func TestFactoryReadsTheOlderAllowances(t *testing.T) {
	routes := routesOf(t, "factory")
	routes["/api/billing/limits"] = `{"usesTokenRateLimitsBilling":false}`
	var queries []string
	srv := factoryServer(t, routes, &queries)
	got, err := Factory{API: srv.URL, App: srv.URL, Now: factoryNow}.Read(context.Background(), "fk-good")
	if err != nil || len(got.Windows) != 2 {
		t.Fatalf("got %+v, %v", got, err)
	}
	end := time.UnixMilli(1700003600000).UTC()
	if w := got.Windows[0]; w.Name != "standard" || !approx(w.UsedPct, 10) || !w.ResetsAt.Equal(end) {
		t.Errorf("standard %+v", w)
	}
	if w := got.Windows[1]; w.Name != "premium" || !approx(w.UsedPct, 10) {
		t.Errorf("premium %+v", w)
	}
	if last := queries[len(queries)-1]; !strings.Contains(last, "userId=user-1") || !strings.Contains(last, "useCache=true") {
		t.Errorf("usage query %q", last)
	}
}

// An expired window with no time left reads 0, not its last share; a Core
// tier with no data is left out; a ratio of 0 with tokens used is computed.
func TestFactoryWindowMapping(t *testing.T) {
	c := factoryClient{now: factoryNow}
	used, zero := 80.0, 0.0
	l := factoryLimits{TokenRateLimits: true}
	l.Limits = &struct {
		Standard *factoryTier `json:"standard"`
		Core     *factoryTier `json:"core"`
	}{
		Standard: &factoryTier{FiveHour: &factoryWindow{UsedPct: &used, End: stamp{factoryNow().Add(-time.Hour)}}},
		Core:     &factoryTier{FiveHour: &factoryWindow{UsedPct: &zero}},
	}
	r := c.rateLimited(l)
	if len(r.Windows) != 1 || r.Windows[0].UsedPct != 0 || !r.Windows[0].ResetsAt.IsZero() {
		t.Errorf("expired window %+v", r.Windows)
	}
	p := factoryPool{UserTokens: number{50, true}, Allowance: number{200, true}, Ratio: number{0, true}}
	if got := p.percent(); got != 25 {
		t.Errorf("computed share = %v", got)
	}
	unlimited := factoryPool{UserTokens: number{5e7, true}, Allowance: number{2e12, true}}
	if got := unlimited.percent(); got != 50 {
		t.Errorf("unlimited share = %v", got)
	}
}

func TestFactoryReadsWithTheSession(t *testing.T) {
	srv := factoryServer(t, routesOf(t, "factory"), nil)
	got, err := FactoryWeb{API: srv.URL, App: srv.URL, Now: factoryNow}.Read(context.Background(), "wos-session=ws; access-token=stale.jwt.token")
	if err != nil || len(got.Windows) != 6 {
		t.Fatalf("session = %+v, %v", got, err)
	}
	if _, err := (FactoryWeb{API: srv.URL, App: srv.URL}).Read(context.Background(), "wos-session=expired"); !errors.Is(err, usage.ErrAuth) {
		t.Errorf("expired session = %v", err)
	}
}

func TestFactoryRefusalsAndSkips(t *testing.T) {
	srv := factoryServer(t, routesOf(t, "factory"), nil)
	if _, err := (Factory{API: srv.URL, App: srv.URL}).Read(context.Background(), "fk-revoked"); !errors.Is(err, usage.ErrAuth) {
		t.Errorf("revoked key = %v", err)
	}
	if _, err := (Factory{API: srv.URL, App: srv.URL}).Read(context.Background(), "wos-session=ws"); !errors.Is(err, usage.ErrSkip) {
		t.Errorf("key reader given a session = %v", err)
	}
	if _, err := (FactoryWeb{API: srv.URL, App: srv.URL}).Read(context.Background(), "fk-good"); !errors.Is(err, usage.ErrSkip) {
		t.Errorf("session reader given a key = %v", err)
	}
}

func TestFactoryUnknownShape(t *testing.T) {
	routes := routesOf(t, "factory")
	routes["/api/billing/limits"] = `{}`
	routes["/api/organization/subscription/usage"] = `{"unexpected":true}`
	srv := factoryServer(t, routes, nil)
	if _, err := (Factory{API: srv.URL, App: srv.URL}).Read(context.Background(), "fk-good"); err == nil || errors.Is(err, usage.ErrAuth) {
		t.Errorf("unknown shape = %v", err)
	}
}
