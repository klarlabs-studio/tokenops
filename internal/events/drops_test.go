package events

import (
	"strings"
	"testing"
)

func TestDropWarningIsSilentWhenNothingWasLost(t *testing.T) {
	for _, n := range []int64{0, -1} {
		if got := DropWarning(n); got != "" {
			t.Fatalf("DropWarning(%d) = %q, want empty", n, got)
		}
	}
}

// The count is the whole point: "some rows were lost" reads the same after
// one dropped batch and after thirty thousand, and the second is an outage.
func TestDropWarningNamesTheCount(t *testing.T) {
	warn := DropWarning(30254)
	if !strings.Contains(warn, "30254") {
		t.Fatalf("warning omits the count: %q", warn)
	}
	// Cumulative since this daemon started, not since the beginning of time —
	// an operator reading it needs to know a restart resets it, or they will
	// read a cleared counter as a fixed problem.
	if !strings.Contains(warn, "started") {
		t.Fatalf("warning does not scope the count to this daemon run: %q", warn)
	}
}

func TestDropWarningSingularReadsAsEnglish(t *testing.T) {
	warn := DropWarning(1)
	if strings.Contains(warn, "1 events") {
		t.Fatalf("warning should not say \"1 events\": %q", warn)
	}
}
