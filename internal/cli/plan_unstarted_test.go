package cli

import (
	"strings"
	"testing"

	"go.klarlabs.de/tokenops/internal/capability/headroom"
)

// An unstarted window has no reset to count down to: the plan says it
// starts at the next request, and a started window keeps its countdown.
func TestPlanWindowLinesSayAnUnstartedWindowStartsAtTheNextRequest(t *testing.T) {
	lines := planWindowLines(headroom.Report{Windows: []headroom.VendorWindow{
		{Name: "week", NotStarted: true},
		{Name: "5h", UsedPct: 10, ResetsIn: "4h0m0s"},
	}})
	if len(lines) != 2 {
		t.Fatalf("lines %q", lines)
	}
	if !strings.Contains(lines[0], "starts at your next request") || strings.Contains(lines[0], "resets in") {
		t.Errorf("unstarted line %q", lines[0])
	}
	if !strings.Contains(lines[1], "resets in 4h") {
		t.Errorf("started line %q", lines[1])
	}
}

// A window that has reset since its reading says so and gives no share:
// the one it had is the last window's.
func TestPlanWindowLinesGiveNoShareForAWindowThatHasReset(t *testing.T) {
	lines := planWindowLines(headroom.Report{Windows: []headroom.VendorWindow{
		{Name: "5h", UsedPct: 90, Lapsed: true},
		{Name: "week", UsedPct: 40, ResetsIn: "48h0m0s"},
	}})
	if len(lines) != 2 {
		t.Fatalf("lines %q", lines)
	}
	if !strings.Contains(lines[0], "5h has reset since it was last read") || strings.Contains(lines[0], "%") {
		t.Errorf("reset line %q", lines[0])
	}
	if !strings.Contains(lines[1], "60% of the week left") {
		t.Errorf("current line %q", lines[1])
	}
}
