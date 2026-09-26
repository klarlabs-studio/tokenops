package plans

import (
	"testing"
	"time"
)

func TestSessionBudgetUnknownPlan(t *testing.T) {
	_, err := ComputeSessionBudget("not-a-plan", SessionBudgetInputs{Now: time.Now().UTC()})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestSessionBudgetPlanWithoutWindowCap(t *testing.T) {
	// gpt-plus publishes model-dependent ranges rather than one static cap.
	out, err := ComputeSessionBudget("gpt-plus", SessionBudgetInputs{
		WindowMessages: 0,
		RecentMessages: 0,
		RecentWindow:   30 * time.Minute,
		Now:            time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("compute: %v", err)
	}
	if out.RecommendedAction != ActionUnknown {
		t.Errorf("recommendation=%q want unknown (no cap published)", out.RecommendedAction)
	}
	if out.Note == "" {
		t.Error("expected explanatory note when no cap published")
	}
}

func TestSessionBudgetAuthoritativeOverridesMessageCount(t *testing.T) {
	// The message-count path would say "continue" (0 messages), but the
	// vendor's own meter reads 87% — the authoritative value must win and
	// drive a slow_down at high confidence.
	out, err := ComputeSessionBudget("claude-max-20x", SessionBudgetInputs{
		WindowMessages: 0, // heuristic would see an empty window
		Authoritative: &AuthoritativeWindow{
			UsedPct: 87, ResetsIn: 42 * time.Minute, Source: "claude_usage_meter:seven_day",
		},
		Now: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("compute: %v", err)
	}
	if out.WindowPct != 87 {
		t.Errorf("window_pct=%v want 87 (vendor meter, not message count)", out.WindowPct)
	}
	if out.RecommendedAction != ActionSlowDown {
		t.Errorf("recommendation=%q want slow_down at 87%%", out.RecommendedAction)
	}
	if out.Confidence != ConfidenceHigh {
		t.Errorf("confidence=%q want high (authoritative)", out.Confidence)
	}
	if out.WindowResetsIn != "42m0s" {
		t.Errorf("resets_in=%q want 42m0s (vendor reset)", out.WindowResetsIn)
	}
	// 13% of the allowance remains, whatever the allowance is.
	wantHeadroom := int64(float64(capOf(t, "claude-max-20x")) * 0.13)
	if diff := out.HeadroomUntilCap - wantHeadroom; diff > 1 || diff < -1 {
		t.Errorf("headroom=%d want ~%d (13%% of %d)",
			out.HeadroomUntilCap, wantHeadroom, capOf(t, "claude-max-20x"))
	}
	if out.Note == "" {
		t.Error("expected a note explaining the vendor-meter source")
	}
}

func TestSessionBudgetAuthoritativeScoresCaplessPlan(t *testing.T) {
	// gpt-plus has a window but no single static message cap — the message-count
	// path returns "unknown", but a vendor % must still produce advice.
	out, err := ComputeSessionBudget("gpt-plus", SessionBudgetInputs{
		Authoritative: &AuthoritativeWindow{UsedPct: 96, Source: "codex:primary"},
		Now:           time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("compute: %v", err)
	}
	if out.RecommendedAction != ActionWaitReset {
		t.Errorf("recommendation=%q want wait_for_reset at 96%%", out.RecommendedAction)
	}
	if out.WindowPct != 96 {
		t.Errorf("window_pct=%v want 96", out.WindowPct)
	}
}

func TestSessionBudgetAuthoritativeUsesVendorShape(t *testing.T) {
	out, err := ComputeSessionBudget("gpt-plus", SessionBudgetInputs{
		Authoritative: &AuthoritativeWindow{
			UsedPct:        23,
			ResetsIn:       6 * 24 * time.Hour,
			Duration:       7 * 24 * time.Hour,
			Source:         "codex:primary",
			VendorPlanType: "prolite",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.WindowDuration != "168h0m0s" || out.VendorPlanType != "prolite" {
		t.Fatalf("budget = %+v", out)
	}
}

func TestSessionBudgetContinueWhenLowUsage(t *testing.T) {
	// Claude Max 20x: 200 msgs / 5h. 20 consumed, 8 in last 30 min.
	out, err := ComputeSessionBudget("claude-max-20x", SessionBudgetInputs{
		WindowMessages: 20,
		RecentMessages: 8,
		RecentWindow:   30 * time.Minute,
		Now:            time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("compute: %v", err)
	}
	if out.RecommendedAction != ActionContinue {
		t.Errorf("recommendation=%q want continue", out.RecommendedAction)
	}
	if out.Confidence != ConfidenceHigh {
		t.Errorf("confidence=%q want high", out.Confidence)
	}
	if out.WillHitCapWithin == "" {
		t.Error("expected ETA when burn rate > 0")
	}
}

func TestSessionBudgetSlowDownWhenHighUsage(t *testing.T) {
	// 85% of the allowance with moderate burn — slow down.
	out, err := ComputeSessionBudget("claude-max-20x", SessionBudgetInputs{
		WindowMessages: pctOf(t, "claude-max-20x", 85),
		RecentMessages: 12,
		RecentWindow:   30 * time.Minute,
		Now:            time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("compute: %v", err)
	}
	if out.RecommendedAction != ActionSlowDown {
		t.Errorf("recommendation=%q want slow_down (got %f%%)", out.RecommendedAction, out.WindowPct)
	}
}

func TestSessionBudgetWaitWhenExhausted(t *testing.T) {
	// Fully consumed, whatever the allowance is.
	out, err := ComputeSessionBudget("claude-max-20x", SessionBudgetInputs{
		WindowMessages: capOf(t, "claude-max-20x"),
		RecentMessages: 5,
		RecentWindow:   30 * time.Minute,
		Now:            time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("compute: %v", err)
	}
	if out.RecommendedAction != ActionWaitReset {
		t.Errorf("recommendation=%q want wait_for_reset", out.RecommendedAction)
	}
	if out.HeadroomUntilCap != 0 {
		t.Errorf("headroom=%d want 0", out.HeadroomUntilCap)
	}
}

func TestSessionBudgetSwitchModelWhenBurnHigh(t *testing.T) {
	// 65% used with a burn rate that empties the remainder inside the
	// switch_model band. The burn is scaled to the allowance for the same
	// reason the consumption is.
	const pct = 65
	used := pctOf(t, "claude-max-20x", pct)
	out, err := ComputeSessionBudget("claude-max-20x", SessionBudgetInputs{
		WindowMessages: used,
		RecentMessages: used / 5,
		RecentWindow:   30 * time.Minute,
		Now:            time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("compute: %v", err)
	}
	if out.RecommendedAction != ActionSwitchModel {
		t.Errorf("recommendation=%q want switch_model (pct=%f rate/h=%f headroom=%d)",
			out.RecommendedAction, out.WindowPct, out.RecentRatePerHour, out.HeadroomUntilCap)
	}
}

func TestSessionBudgetLowConfidenceWithoutHistory(t *testing.T) {
	out, err := ComputeSessionBudget("claude-max-20x", SessionBudgetInputs{
		WindowMessages: pctOf(t, "claude-max-20x", 30),
		RecentMessages: 0,
		RecentWindow:   0,
		Now:            time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("compute: %v", err)
	}
	if out.Confidence != ConfidenceLow {
		t.Errorf("confidence=%q want low when no history", out.Confidence)
	}
}

// capOf is the plan's per-window allowance. The budget tests are about where
// the recommendation bands fall — at 85% slow down, at 100% wait — not about
// any particular cap, so they express consumption as a fraction of whatever
// the catalog says rather than hardcoding a number. They used to hardcode
// 200, which is what tied them to an absolute that turned out to disagree
// with the multiplier the vendor documents.
func capOf(t *testing.T, plan string) int64 {
	t.Helper()
	p, ok := Lookup(plan)
	if !ok {
		t.Fatalf("plan %q missing", plan)
	}
	if p.MessagesPerWindow <= 0 {
		t.Fatalf("plan %q has no per-window allowance", plan)
	}
	return p.MessagesPerWindow
}

// pctOf is n percent of the plan's allowance, rounded down.
func pctOf(t *testing.T, plan string, pct float64) int64 {
	t.Helper()
	return int64(float64(capOf(t, plan)) * pct / 100)
}
