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
	if st.Title != "Codex 49%" || st.Pct != 49 {
		t.Fatalf("status %+v", st)
	}
	lines := st.Tooltip
	for _, want := range []string{"Claude · week 15% · resets in 6d 2h", "Claude · 5h 6% · resets in 4h 25m", "Codex · week 49%", "Fireworks · $41.50 of $100.00"} {
		if !strings.Contains(lines, want) {
			t.Errorf("tooltip lacks %q:\n%s", want, lines)
		}
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
	img, err := png.Decode(bytes.NewReader(trayIcon(25)))
	if err != nil || img.Bounds().Dx() != iconSize {
		t.Fatalf("icon %v %v", img, err)
	}
	if _, _, _, a := img.At(0, 0).RGBA(); a != 0 {
		t.Error("corner not transparent")
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
