package mcp

import (
	"strings"
	"testing"

	"go.klarlabs.de/tokenops/internal/capability/headroom"
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

// A window that has reset since its reading is named without the share it
// held: that share is the last window's.
func TestBudgetSummaryGivesNoShareForAWindowThatHasReset(t *testing.T) {
	out := renderBudgetSummary(budgetSummaryRow{Display: "ChatGPT Plus", Windows: []headroom.VendorWindow{
		{Name: "5h", UsedPct: 90, Lapsed: true}, {Name: "week", UsedPct: 40, ResetsIn: "50h0m0s"},
	}})
	if !strings.Contains(out, "| 5h | reset since it was last read | |") || strings.Contains(out, "10%") || !strings.Contains(out, "| week | 60% | 50h0m0s |") {
		t.Errorf("%s", out)
	}
}
