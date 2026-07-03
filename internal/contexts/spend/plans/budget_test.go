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

func TestSessionBudgetAuthoritativeOverridesMessageCount(t *testing.T) {
	// The message-count path would say "continue" (0 messages), but the
	// vendor's own meter reads 87% — the authoritative value must win and
	// drive a slow_down at high confidence.
	out, err := ComputeSessionBudget("claude-max-20x", SessionBudgetInputs{
		WindowMessages: 0, // heuristic would see an empty window
		Authoritative: &AuthoritativeWindow{
			UsedPct: 87, ResetsIn: 42 * time.Minute, Source: "anthropic_cookie:seven_day",
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
	// 20x cap is 200 msgs; 13% headroom ≈ 26.
	if out.HeadroomUntilCap != 26 {
		t.Errorf("headroom=%d want ~26 (13%% of 200)", out.HeadroomUntilCap)
	}
	if out.Note == "" {
		t.Error("expected a note explaining the vendor-meter source")
	}
}

func TestSessionBudgetAuthoritativeScoresCaplessPlan(t *testing.T) {
	// claude-code-max has a window but no message cap — the message-count
	// path returns "unknown", but a vendor % must still produce advice.
	out, err := ComputeSessionBudget("claude-code-max", SessionBudgetInputs{
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
