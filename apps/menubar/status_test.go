package main

import (
	"bytes"
	"context"
	"errors"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const glanceSample = `{"insight":{"level":"clear","summary":"Claude Max 20x recommends continuing."},
 "plan_headroom":{"reports":[
  {"provider":"anthropic","display":"Claude Max 20x","windows":[
    {"name":"5h","used_pct":6,"resets_in":"4h25m0s"},{"name":"week","used_pct":15,"resets_in":"146h45m0s"}]},
  {"provider":"openai","display":"ChatGPT Pro","windows":[{"name":"week","used_pct":49}]},
  {"provider":"fireworks","display":"Pay as you go","spend_usd":41.5,"spend_limit_usd":100,"spend_pct":41.5}]}}`

// The tray shows whatever stops work first, across every plan.
func TestStatusShowsTheBusiestWindow(t *testing.T) {
	st := statusOf([]byte(glanceSample))
	if st.Title != "Codex 51% left" || st.Pct != 49 || st.Outer != 49 || st.Inner != -1 {
		t.Fatalf("status %+v", st)
	}
	lines := st.Tooltip
	for _, want := range []string{"Claude · week 85% left · resets in 6d 2h", "Claude · 5h 94% left · resets in 4h 25m", "Codex · week 51% left", "Fireworks · $41.50 of $100.00"} {
		if !strings.Contains(lines, want) {
			t.Errorf("tooltip lacks %q:\n%s", want, lines)
		}
	}
}

// The icon's rings are the busiest plan's week (outer) and session (inner).
func TestStatusRingsAreTheBusiestPlansWindows(t *testing.T) {
	st := statusOf([]byte(`{"plan_headroom":{"reports":[
	  {"provider":"anthropic","windows":[{"name":"5h","used_pct":70},{"name":"week","used_pct":20},{"name":"week (Fable)","used_pct":35}]},
	  {"provider":"openai","windows":[{"name":"week","used_pct":40}]}]}}`))
	if st.Outer != 35 || st.Inner != 70 || st.Pct != 70 {
		t.Errorf("status %+v", st)
	}
	st = statusOf([]byte(`{"plan_headroom":{"reports":[{"provider":"anthropic","windows":[{"name":"5h","used_pct":30}]}]}}`))
	if st.Outer != 30 || st.Inner != -1 {
		t.Errorf("a session window alone is the one ring: %+v", st)
	}
}

func TestStatusWithNothingToShow(t *testing.T) {
	if st := statusOf([]byte(`{"plan_headroom":{"error":"plans_unconfigured","hint":"bind a plan"}}`)); st.Title != "—" || !strings.Contains(st.Tooltip, "bind a plan") {
		t.Errorf("unconfigured %+v", st)
	}
	if st := statusOf([]byte(`not json`)); st.Title != "—" {
		t.Errorf("garbage %+v", st)
	}
}

func TestHumanDuration(t *testing.T) {
	for in, want := range map[string]string{"146h45m0s": "6d 2h", "4h25m0s": "4h 25m", "12m30s": "13m", "nonsense": "nonsense"} {
		if got := humanDuration(in); got != want {
			t.Errorf("%s → %q, want %q", in, got, want)
		}
	}
}

func TestTrayIconIsATemplate(t *testing.T) {
	img, err := png.Decode(bytes.NewReader(trayIcon(25, 50)))
	if err != nil || img.Bounds().Dx() != iconSize {
		t.Fatalf("icon %v %v", img, err)
	}
	alpha := func(x, y int) uint32 { _, _, _, a := img.At(x, y).RGBA(); return a >> 8 }
	if alpha(0, 0) != 0 || alpha(18, 18) != 0 {
		t.Error("corner or centre not transparent")
	}
	// 25% used leaves 75% of the outer ring: the right side (a quarter of
	// the way round) is filled, the top-left (seven eighths) is track.
	if alpha(33, 18) != 0xff || alpha(7, 7) != trackAlpha {
		t.Errorf("outer ring: right %x, top-left %x", alpha(33, 18), alpha(7, 7))
	}
	// 50% used leaves the inner ring's right half: below the centre on the
	// right is filled, on the left is track.
	if alpha(25, 22) != 0xff || alpha(11, 22) != trackAlpha {
		t.Errorf("inner ring: right %x, left %x", alpha(25, 22), alpha(11, 22))
	}
}

// The client sends the daemon's token, and tells a daemon that is not
// there from one that is slow.
func TestDaemonClient(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.URL.Path == "/api/slow" {
			time.Sleep(200 * time.Millisecond)
		}
		_, _ = w.Write([]byte(glanceSample))
	}))
	defer srv.Close()
	dir := t.TempDir()
	d := &daemon{urlFile: filepath.Join(dir, "daemon.url"), hc: &http.Client{Timeout: 50 * time.Millisecond}}
	ctx := context.Background()

	if _, err := d.glance(ctx); !errors.Is(err, errNoDaemon) {
		t.Fatalf("no url file: %v", err)
	}
	if err := os.WriteFile(d.urlFile, []byte(`{"url":"`+srv.URL+`","dashboard_token":"tok"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if g, err := d.glance(ctx); err != nil || !strings.Contains(string(g), "Claude Max 20x") {
		t.Fatalf("glance %s %v", g, err)
	}
	var out map[string]any
	if err := d.do(ctx, http.MethodGet, "/api/slow", nil, &out); !errors.Is(err, errSlow) {
		t.Errorf("slow daemon: %v", err)
	}
	srv.Close()
	if _, err := d.glance(ctx); !errors.Is(err, errNoDaemon) {
		t.Errorf("stopped daemon: %v", err)
	}
}

// Spend reads one provider's window and counts the requests whose model
// has no list price, which the money leaves out.
func TestSpendSince(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/spend/summary" || r.URL.Query().Get("provider") != "openai" || r.URL.Query().Get("since") != "720h" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		_, _ = w.Write([]byte(`{"summary":{"Requests":10,"TotalTokens":2240000000,"CostUSD":0,"APIEquivalentUSD":5.83,
			"Unpriced":[{"Model":"gpt-6.1-sol","Requests":6},{"Model":"codex-auto-review","Requests":2}]}}`))
	}))
	defer srv.Close()
	dir := t.TempDir()
	d := &daemon{urlFile: filepath.Join(dir, "daemon.url"), hc: srv.Client()}
	if err := os.WriteFile(d.urlFile, []byte(`{"url":"`+srv.URL+`","dashboard_token":"tok"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := d.spendSince(context.Background(), "openai", "720h")
	if err != nil {
		t.Fatal(err)
	}
	want := spend{Tokens: 2240000000, APIEquivalent: 5.83, Requests: 10, Unpriced: 8}
	if got != want {
		t.Errorf("spend = %+v, want %+v", got, want)
	}
}

// The chart is a provider's days oldest first; the top model is the one
// with the most tokens over them.
func TestDailyAndTopModel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if r.URL.Path != "/api/spend/series" || q.Get("provider") != "openai" || q.Get("bucket") != "day" || q.Get("since") != "720h" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if q.Get("group") == "model" {
			_, _ = w.Write([]byte(`{"rows":[{"GroupKey":"gpt-5.5","TotalTokens":300},{"GroupKey":"gpt-6","TotalTokens":250},{"GroupKey":"gpt-6","TotalTokens":100}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"rows":[{"BucketStart":"2026-10-05T00:00:00Z","TotalTokens":7,"APIEquivalentUSD":1.5},{"BucketStart":"2026-10-04T00:00:00Z","TotalTokens":3}]}`))
	}))
	defer srv.Close()
	d := &daemon{urlFile: filepath.Join(t.TempDir(), "daemon.url"), hc: srv.Client()}
	if err := os.WriteFile(d.urlFile, []byte(`{"url":"`+srv.URL+`","dashboard_token":"tok"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	days, err := d.daily(context.Background(), "openai")
	if err != nil || len(days) != 2 || days[0].Date != "2026-10-04" || days[1].Tokens != 7 || days[1].APIEquivalent != 1.5 {
		t.Fatalf("daily %+v %v", days, err)
	}
	if top, err := d.topModel(context.Background(), "openai"); err != nil || top != "gpt-6" {
		t.Errorf("top model %q %v", top, err)
	}
}

// Refresh asks the daemon to poll now. A refresh too soon after the last
// says when the next is allowed, and an older daemon without the route
// is not an error: the panel re-reads what it has.
func TestRefreshSources(t *testing.T) {
	answer := http.StatusAccepted
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/sources/refresh" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(answer)
		switch answer {
		case http.StatusAccepted:
			_, _ = w.Write([]byte(`{"requested":true,"pollers":9,"next_at":"2026-10-07T12:00:30Z"}`))
		case http.StatusTooManyRequests:
			_, _ = w.Write([]byte(`{"requested":false,"pollers":0,"next_at":"2026-10-07T12:00:30Z"}`))
		}
	}))
	defer srv.Close()
	dir := t.TempDir()
	d := &daemon{urlFile: filepath.Join(dir, "daemon.url"), hc: &http.Client{Timeout: time.Second}}
	if err := os.WriteFile(d.urlFile, []byte(`{"url":"`+srv.URL+`","dashboard_token":"tok"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if r, err := d.refreshSources(ctx); err != nil || !r.Requested || r.Pollers != 9 {
		t.Errorf("accepted: %+v %v", r, err)
	}
	answer = http.StatusTooManyRequests
	if r, err := d.refreshSources(ctx); err != nil || r.Requested || r.NextAt.IsZero() {
		t.Errorf("too soon: %+v %v", r, err)
	}
	answer = http.StatusNotFound
	if r, err := d.refreshSources(ctx); err != nil || r.Requested || !r.Unsupported {
		t.Errorf("older daemon: %+v %v", r, err)
	}
}

func TestRefreshNote(t *testing.T) {
	now := time.Date(2026, 10, 7, 12, 0, 10, 0, time.UTC)
	cases := map[string]sourcesRefresh{
		"Asked 9 readers to poll now; new readings arrive as they answer.": {Requested: true, Pollers: 9},
		"Refreshed moments ago; the next refresh is allowed in 20s.":       {NextAt: now.Add(20 * time.Second)},
		"This daemon cannot poll on demand; showing its latest readings.":  {Unsupported: true},
	}
	for want, r := range cases {
		if got := r.note(now); got != want {
			t.Errorf("note(%+v) = %q, want %q", r, got, want)
		}
	}
}
