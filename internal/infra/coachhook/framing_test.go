package coachhook

import (
	"strings"
	"testing"
)

// The nudge said "your $50 budget" to an operator paying $200/month for
// Claude Max. Three things were wrong at once: they never chose $50, the
// figure is API-equivalent rather than money anyone is charged, and a
// per-session ceiling has nothing to do with a monthly subscription
// price. Read together it looked like a bill that did not add up.
func TestNudgeDoesNotClaimTheBudgetIsTheirs(t *testing.T) {
	for _, frac := range []float64{0.5, 0.75, 1.0, 2.0} {
		msg := nudgeMessage(frac, 40, DefaultBudgetUSD, false)
		if strings.Contains(msg, "your $50") || strings.Contains(msg, "your default") {
			t.Errorf("frac %.2f claims the default is the operator's choice: %q", frac, msg)
		}
	}
}

// Every tier must say the figure is API-equivalent. Only the lowest one
// did, so the louder the warning got, the more it read like real money.
func TestEveryNudgeNamesTheUnit(t *testing.T) {
	for _, frac := range []float64{0.5, 0.75, 1.0, 2.0} {
		msg := nudgeMessage(frac, 40, DefaultBudgetUSD, false)
		if !strings.Contains(msg, "API-equivalent") {
			t.Errorf("frac %.2f does not say what the number is: %q", frac, msg)
		}
	}
}

// A budget the operator actually configured can be called theirs.
func TestConfiguredBudgetIsCalledTheirs(t *testing.T) {
	msg := nudgeMessage(0.75, 40, 60, true)
	if !strings.Contains(msg, "your") {
		t.Errorf("a configured budget should be addressed as theirs: %q", msg)
	}
}

// On a subscription the dollar figure is a counterfactual — the operator
// is billed a flat fee whatever this says. The nudge has to be explicit
// that it is not a charge, or it reads as one.
func TestNudgeSaysItIsNotACharge(t *testing.T) {
	msg := nudgeMessage(1.0, 55, DefaultBudgetUSD, false)
	lowered := strings.ToLower(msg)
	if !strings.Contains(lowered, "not a charge") && !strings.Contains(lowered, "not billed") {
		t.Errorf("message must not read as a bill: %q", msg)
	}
}

// The lever still has to be named — the point is to act, not just to be
// informed.
func TestNudgeStillNamesTheLever(t *testing.T) {
	for _, frac := range []float64{0.5, 0.75, 1.0, 2.0} {
		msg := nudgeMessage(frac, 40, DefaultBudgetUSD, false)
		if !strings.Contains(msg, "/compact") {
			t.Errorf("frac %.2f does not name the lever: %q", frac, msg)
		}
	}
}
