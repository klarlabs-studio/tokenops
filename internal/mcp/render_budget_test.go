package mcp

import (
	"strings"
	"testing"
)

// Codex reports a window as a percentage with no cap; it still renders.
func TestBudgetSummaryShowsAPercentOnlyWindow(t *testing.T) {
	out := renderBudgetSummary(budgetSummaryRow{Display: "ChatGPT Pro", WindowPct: 14, WindowResetsIn: "164h9m0s"})
	if !strings.Contains(out, "86.0% left") || !strings.Contains(out, "164h9m0s") {
		t.Errorf("%s", out)
	}
}
