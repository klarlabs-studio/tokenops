package mcp

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/storage/sqlite"
)

func planDepsFor(t *testing.T, bindings map[string]string) PlanDeps {
	t.Helper()
	st, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "p.db"), sqlite.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return PlanDeps{Config: &config.Config{Plans: bindings}, Store: st}
}

// An Enterprise plan has no rate-limit window, so it was skipped and the
// agent got {"budgets":[]} — indistinguishable from "nothing configured".
// The answer has to say why it is empty and where the spend view lives.
func TestSessionBudgetExplainsASpendBilledPlan(t *testing.T) {
	out, err := sessionBudget(context.Background(),
		planDepsFor(t, map[string]string{"anthropic": "claude-enterprise"}))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "tokenops_plan_headroom") {
		t.Errorf("empty budget with no pointer to the spend view:\n%s", out)
	}
	if !strings.Contains(out, "claude-enterprise") {
		t.Errorf("note should name the plan it skipped:\n%s", out)
	}
}

// The markdown renders budgets[0], and budgets were built by ranging a
// map, so with two plans the rendered one changed from call to call.
func TestSessionBudgetRendersTheSamePlanEveryCall(t *testing.T) {
	d := planDepsFor(t, map[string]string{
		"openai":    "gpt-plus",
		"anthropic": "claude-max-20x",
	})
	for range 20 { // map order is randomised per range; one pass proves nothing
		out, err := sessionBudget(context.Background(), d)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(out, "## Claude Max 20x") {
			head, _, _ := strings.Cut(out, "\n")
			t.Fatalf("rendered %q, want the first provider in sorted order (anthropic)", head)
		}
	}
}

// Config hot-reloads, so "reload your MCP server" was a step that changed
// nothing, and the hint named only the CLI when the agent reading it can
// bind the plan itself.
func TestPlanHintsNameTheMCPToolAndDropTheReload(t *testing.T) {
	d := PlanDeps{Config: &config.Config{}}
	budget, err := sessionBudget(context.Background(), d)
	if err != nil {
		t.Fatal(err)
	}
	headroom, err := planHeadroom(context.Background(), d)
	if err != nil {
		t.Fatal(err)
	}
	for name, hint := range map[string]string{"session_budget": budget, "plan_headroom": headroom.Hint} {
		if !strings.Contains(hint, "tokenops_plan_set") {
			t.Errorf("%s hint does not name tokenops_plan_set: %s", name, hint)
		}
		if strings.Contains(hint, "reload your MCP server") {
			t.Errorf("%s hint still asks for a reload: %s", name, hint)
		}
	}
}
