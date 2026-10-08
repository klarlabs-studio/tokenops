package accounts

import (
	"context"
	"errors"
	"testing"
	"time"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
)

// The fixture is the answer CodexBar's OpenCodeGoUsageFetcherErrorTests
// serves for /zen/go/v1/usage: percentages already 0–100, resets as times.
func TestOpencodeGoReadsWindows(t *testing.T) {
	srv := serve(t, "/zen/go/v1/usage", "go_secret", fixture(t, "opencode-go"))
	defer srv.Close()
	got, err := OpencodeGo{BaseURL: srv.URL}.Read(context.Background(), "go_secret")
	if err != nil || !got.Subscription || len(got.Windows) != 3 {
		t.Fatalf("got %+v, %v", got, err)
	}
	want := []struct {
		name  string
		pct   float64
		d     time.Duration
		reset string
	}{
		{"5h", 12, 5 * time.Hour, "2026-08-12T02:00:00Z"},
		{"week", 8, 7 * 24 * time.Hour, "2026-08-18T00:00:00Z"},
		{"month", 35, 30 * 24 * time.Hour, "2026-09-01T00:00:00Z"},
	}
	for i, w := range want {
		g := got.Windows[i]
		if g.Name != w.name || !approx(g.UsedPct, w.pct) || g.Duration != w.d || g.ResetsAt.Format(time.RFC3339) != w.reset {
			t.Errorf("window %d = %+v, want %+v", i, g, w)
		}
	}
}

func TestOpencodeGoWindowShapes(t *testing.T) {
	now := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		body  string
		pct   float64
		reset time.Time
		ok    bool
	}{
		{`{"usagePercent": 0.5, "resetInSec": 600}`, 0.5, now.Add(10 * time.Minute), true},
		{`{"used": 3, "limit": 12}`, 25, time.Time{}, true},
		{`{"percent": 140}`, 100, time.Time{}, true},
		{`{"status": "ok"}`, 0, time.Time{}, false},
		{``, 0, time.Time{}, false},
	}
	for _, c := range cases {
		w, ok := opencodeGoWindow([]byte(c.body), now)
		if ok != c.ok || !approx(w.UsedPct, c.pct) || !w.ResetsAt.Equal(c.reset) {
			t.Errorf("%s: got %+v %v", c.body, w, ok)
		}
	}
}

func TestOpencodeGoOnlyRollingIsRequired(t *testing.T) {
	srv := serve(t, "/zen/go/v1/usage", "k", `{"usage":{"rolling":{"percent":1,"resetsAt":"2026-08-12T02:00:00Z"}}}`)
	defer srv.Close()
	got, err := OpencodeGo{BaseURL: srv.URL}.Read(context.Background(), "k")
	if err != nil || len(got.Windows) != 1 || got.Windows[0].Name != "5h" {
		t.Fatalf("got %+v, %v", got, err)
	}
}

func TestOpencodeGoRefusedAndUnknown(t *testing.T) {
	srv := serve(t, "/zen/go/v1/usage", "k", `{"error":"no subscription"}`)
	defer srv.Close()
	if _, err := (OpencodeGo{BaseURL: srv.URL}).Read(context.Background(), "bad"); !errors.Is(err, usage.ErrAuth) {
		t.Errorf("refused key: %v", err)
	}
	if _, err := (OpencodeGo{BaseURL: srv.URL}).Read(context.Background(), "k"); !errors.Is(err, errNoGoWindows) {
		t.Errorf("no windows: %v", err)
	}
}
