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

// A capped window shows the count and the share left, never the share
// used: plan windows read as a fuel gauge on every surface.
func TestBudgetSummaryShowsACappedWindowAsShareLeft(t *testing.T) {
	out := renderBudgetSummary(budgetSummaryRow{Display: "Claude Pro", WindowConsumed: 30, WindowCap: 100, WindowUnit: "messages", WindowPct: 30, WindowResetsIn: "2h"})
	if !strings.Contains(out, "30 / 100 messages (70.0% left)") || strings.Contains(out, "(30.0%)") {
		t.Errorf("%s", out)
	}
}
