package mcp

import (
	"context"
	"path/filepath"
	"testing"

	"go.klarlabs.de/tokenops/internal/capability/headroom"
	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/storage/sqlite"
)

// tokenops_glance (view=plan_advice) reaches the plan advice with its
// period; with no plan bound it answers {error, hint} like the other views.
func TestGlancePlanAdviceView(t *testing.T) {
	srv := fullServer(t)
	if err := Consolidate(srv); err != nil {
		t.Fatal(err)
	}
	out, err := callPublic(t, srv, "tokenops_glance", map[string]any{"view": "plan_advice", "days": 60})
	if err != nil || out["view"] != "plan_advice" {
		t.Fatalf("glance plan_advice = %v %v", out, err)
	}
	if a, _ := out["plan_advice"].(map[string]any); a["error"] != headroom.ErrPlansUnconfigured {
		t.Errorf("plan_advice = %v", out["plan_advice"])
	}
}

func TestPlanAdviceToolReplaysTheBoundPlan(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	st, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "a.db"), sqlite.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	d := PlanDeps{Config: &config.Config{Plans: map[string]string{"anthropic": "claude-max"}}, Store: st}
	res, err := planAdvice(context.Background(), d, planAdviceInput{Days: 45})
	if err != nil {
		t.Fatal(err)
	}
	if res.Days != 45 || len(res.Providers) != 1 || res.Providers[0].Plan != "claude-max" {
		t.Fatalf("advice = %+v", res)
	}
	if _, err := planAdvice(context.Background(), d, planAdviceInput{Days: 120}); err == nil {
		t.Error("a period past 90 days is refused")
	}
}
