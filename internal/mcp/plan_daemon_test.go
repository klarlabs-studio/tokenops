package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// The plan views answer with the daemon's figures when it answers, and
// compute their own only when it does not: a server the client started
// days ago otherwise answers with that release's code.
func TestPlanViewsPreferTheDaemon(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	seen := map[string]string{}
	daemon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen[r.URL.Path] = r.URL.RawQuery
		note := []string{"from the daemon"}
		var body any
		switch r.URL.Path {
		case "/api/glance":
			body = map[string]any{"insight": map[string]any{"summary": "from the daemon"}}
		case "/api/plans/headroom", "/api/plans/session-budget", "/api/plans/advice":
			body = map[string]any{"notes": note}
		case "/api/findings":
			body = map[string]any{"findings": []any{}, "sessions_read_at": time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC)}
		default:
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(body)
	}))
	defer daemon.Close()
	ctx := context.Background()
	d := PlanDeps{Daemon: DaemonPlanReader(daemon.URL)}

	g, err := resourceGlance(ctx, d)
	if err != nil || g.Insight.Summary != "from the daemon" {
		t.Errorf("glance = %+v, %v", g, err)
	}
	h, err := planHeadroom(ctx, d)
	if err != nil || len(h.Notes) != 1 || h.Notes[0] != "from the daemon" {
		t.Errorf("headroom = %+v, %v", h, err)
	}
	b, err := sessionBudgetData(ctx, d)
	if err != nil || len(b.Notes) != 1 {
		t.Errorf("session budget = %+v, %v", b, err)
	}
	a, err := planAdvice(ctx, d, planAdviceInput{Days: 14})
	if err != nil || len(a.Notes) != 1 || seen["/api/plans/advice"] != "days=14" {
		t.Errorf("advice = %+v, %v; query %q", a, err, seen["/api/plans/advice"])
	}

	// A daemon that fails is not the answer: the view is computed here.
	down := PlanDeps{Daemon: func(context.Context, string, any) error { return context.DeadlineExceeded }}
	h, err = planHeadroom(ctx, down)
	if err != nil || len(h.Notes) == 1 && h.Notes[0] == "from the daemon" || !strings.Contains(h.Hint+h.Error, "plan") {
		t.Errorf("headroom without a daemon = %+v, %v; want the local answer", h, err)
	}
}
