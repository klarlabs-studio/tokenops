package coachhook

import (
	"strings"
	"testing"
)

// Dollars are a counterfactual on a subscription. Context is not: it is
// the thing that actually fills up, forces a compaction, and makes every
// subsequent turn re-read more. It belongs in the nudge.
func TestNudgeReportsContextAgainstWindow(t *testing.T) {
	msg := contextNote(873_000, "claude-opus-5")
	if !strings.Contains(msg, "873k") {
		t.Errorf("should report the context size: %q", msg)
	}
	if !strings.Contains(msg, "1.0M") && !strings.Contains(msg, "1M") {
		t.Errorf("should name the window it is measured against: %q", msg)
	}
	if !strings.Contains(msg, "87%") {
		t.Errorf("should give the share used: %q", msg)
	}
}

// An unknown model has no known window, so no percentage can honestly be
// computed. Report the raw size and say nothing about a share — a
// percentage against a guessed denominator looks authoritative and is not.
func TestNudgeOmitsShareForUnknownModel(t *testing.T) {
	msg := contextNote(400_000, "some-future-model")
	if !strings.Contains(msg, "400k") {
		t.Errorf("should still report the size: %q", msg)
	}
	if strings.Contains(msg, "%") {
		t.Errorf("must not invent a share for an unknown window: %q", msg)
	}
}

// Nothing measured, nothing said.
func TestNudgeSkipsContextWhenUnmeasured(t *testing.T) {
	if got := contextNote(0, "claude-opus-5"); got != "" {
		t.Errorf("no context observed should produce no note, got %q", got)
	}
}

// The guidance has to change with the pressure — "consider compacting" at
// 30% would be noise, and silence at 95% would be negligent.
func TestContextAdviceEscalates(t *testing.T) {
	roomy := contextNote(200_000, "claude-opus-5")
	tight := contextNote(950_000, "claude-opus-5")
	if strings.Contains(strings.ToLower(roomy), "compact now") {
		t.Errorf("20%% of the window should not demand action: %q", roomy)
	}
	if !strings.Contains(strings.ToLower(tight), "compact") {
		t.Errorf("95%% of the window should name the lever: %q", tight)
	}
}
