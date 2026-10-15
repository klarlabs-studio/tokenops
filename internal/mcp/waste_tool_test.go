package mcp

import "testing"

// tokenops_spend (view=waste) answers from the waste taxonomy, as the
// daemon's GET /api/spend/waste does (ADR 0010).
func TestSpendWasteView(t *testing.T) {
	srv := fullServer(t)
	if err := Consolidate(srv); err != nil {
		t.Fatal(err)
	}
	out, err := callPublic(t, srv, "tokenops_spend", map[string]any{"view": "waste", "since": "7d"})
	if err != nil || out["view"] != "waste" {
		t.Fatalf("spend waste = %v %v", out, err)
	}
	w, _ := out["waste"].(map[string]any)
	for _, k := range []string{"usage", "avoidable", "classes", "realized", "guardrails", "attribution"} {
		if _, ok := w[k]; !ok {
			t.Errorf("waste has no %q: %v", k, w)
		}
	}
	if classes, _ := w["classes"].([]any); len(classes) != 6 {
		t.Errorf("classes = %v", w["classes"])
	}
	if _, err := callPublic(t, srv, "tokenops_spend", map[string]any{"view": "waste", "since": "yesterday"}); err == nil {
		t.Error("a since that is not a time or duration is refused")
	}
}
