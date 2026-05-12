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
	// claude-code-max publishes no concrete window cap in the catalog.
	out, err := ComputeSessionBudget("claude-code-max", SessionBudgetInputs{
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
	// 170 of 200 msgs (85%) with moderate burn — slow down.
	out, err := ComputeSessionBudget("claude-max-20x", SessionBudgetInputs{
		WindowMessages: 170,
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
	out, err := ComputeSessionBudget("claude-max-20x", SessionBudgetInputs{
		WindowMessages: 200,
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
	// 65% used, burning 40 msgs/h, 70 msgs left -> 70/40 = 1.75h < 1.5h * sometimes.
	// Tune to land in switch_model band: 65% pct, headroom 70, rate 50/h -> 1.4h < 1.5
	out, err := ComputeSessionBudget("claude-max-20x", SessionBudgetInputs{
		WindowMessages: 130,
		RecentMessages: 25,
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
		WindowMessages: 60,
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
