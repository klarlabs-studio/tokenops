package fxrate

import (
	"context"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/contexts/spend/fx"
)

const sample = `<Envelope><Cube><Cube time='2026-09-30'><Cube currency='USD' rate='1.1355'/><Cube currency='GBP' rate='0.86'/></Cube></Cube></Envelope>`

func sandbox(t *testing.T) (calls *int) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	n := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n++
		_, _ = w.Write([]byte(sample))
	}))
	t.Cleanup(srv.Close)
	old := ECBURL
	ECBURL = srv.URL
	t.Cleanup(func() { ECBURL = old })
	return &n
}

func TestResolveFetchesOnceADay(t *testing.T) {
	calls := sandbox(t)
	now := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	m := config.MoneyConfig{Currency: "EUR"}
	r, ok, warn := Resolve(context.Background(), m, now)
	if !ok || warn != "" || r.Source != fx.SourceECB || math.Abs(r.PerUSD-1/1.1355) > 1e-9 {
		t.Fatalf("Resolve = %+v %v %q", r, ok, warn)
	}
	if _, ok, _ := Resolve(context.Background(), m, now.Add(time.Hour)); !ok || *calls != 1 {
		t.Fatalf("refetched within a day: %d calls", *calls)
	}
	if _, _, _ = Resolve(context.Background(), m, now.Add(25*time.Hour)); *calls != 2 {
		t.Fatalf("stale cache not refreshed: %d calls", *calls)
	}
	home, _ := os.UserHomeDir()
	if info, err := os.Stat(filepath.Join(home, ".tokenops", "fx-ecb.json")); err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("cache: %v %v", info, err)
	}
}

func TestPinnedRateAndUSDNeedNoNetwork(t *testing.T) {
	calls := sandbox(t)
	if r, ok, _ := Resolve(context.Background(), config.MoneyConfig{Currency: "EUR", PerUSD: 0.9}, time.Now()); !ok || r.Source != fx.SourceConfig || r.PerUSD != 0.9 {
		t.Errorf("pinned = %+v", r)
	}
	if r, ok, _ := Resolve(context.Background(), config.MoneyConfig{}, time.Now()); !ok || !r.IsUSD() {
		t.Errorf("usd = %+v", r)
	}
	off := false
	if _, ok, warn := Resolve(context.Background(), config.MoneyConfig{Currency: "EUR", FetchRate: &off}, time.Now()); ok || !strings.Contains(warn, "fetch_rate") {
		t.Errorf("fetch off: ok=%v warn=%q", ok, warn)
	}
	if *calls != 0 {
		t.Fatalf("fetched %d times", *calls)
	}
}

func TestFailedFetchFallsBackToTheCache(t *testing.T) {
	sandbox(t)
	now := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	m := config.MoneyConfig{Currency: "EUR"}
	if _, ok, _ := Resolve(context.Background(), m, now); !ok {
		t.Fatal("first fetch failed")
	}
	ECBURL = "http://127.0.0.1:1/unreachable"
	r, ok, warn := Resolve(context.Background(), m, now.Add(48*time.Hour))
	if !ok || r.Date != "2026-09-30" || !strings.Contains(warn, "2026-09-30") {
		t.Fatalf("fallback = %+v %v %q", r, ok, warn)
	}
}
