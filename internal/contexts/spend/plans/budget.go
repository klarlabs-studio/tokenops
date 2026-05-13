package plans

import (
	"math"
	"time"
)

// SessionBudget answers the question Claude Code / Cursor agents
// actually want before starting a long task: "given how I've been
// burning the rate-limit window, will I hit the cap soon — and what
// should I do about it?". Extrapolation is intentionally simple
// (linear) so the recommendation stays explainable; sophistication
// can come later once we know which knob users actually act on.
type SessionBudget struct {
	PlanName          string  `json:"plan_name"`
	Display           string  `json:"display"`
	Provider          string  `json:"provider"`
	WindowCap         int64   `json:"window_cap"`
	WindowConsumed    int64   `json:"window_consumed"`
	WindowPct         float64 `json:"window_pct"`
	WindowResetsIn    string  `json:"window_resets_in"`
	RecentRatePerHour float64 `json:"recent_rate_per_hour"`
	WillHitCapWithin  string  `json:"will_hit_cap_within,omitempty"`
	HeadroomUntilCap  int64   `json:"headroom_until_cap"`
	Confidence        string  `json:"confidence"`
	RecommendedAction string  `json:"recommended_action"`
	// SignalQuality types the trust the operator can place in this
	// prediction. Level=low means the math runs on MCP-ping activity
	// only; the Caveat explains. ClassifySignal computes it.
	SignalQuality SignalQuality `json:"signal_quality"`
	Note          string        `json:"note,omitempty"`

	windowResetsInDur   time.Duration
	willHitCapWithinDur time.Duration
}

// Recommended actions surface to the agent or operator. Closed set so
// dashboards / Claude Code prompts can dispatch on them.
const (
	ActionContinue    = "continue"
	ActionSlowDown    = "slow_down"
	ActionSwitchModel = "switch_model"
	ActionWaitReset   = "wait_for_reset"
	ActionUnknown     = "unknown"
)

// Confidence levels reflect how much history we used to extrapolate.
const (
	ConfidenceLow    = "low"
	ConfidenceMedium = "medium"
	ConfidenceHigh   = "high"
)

// SessionBudgetInputs is the pure-function input the budget tool
// receives from the consumption + catalog layers. Now is injected for
// determinism in tests.
type SessionBudgetInputs struct {
	WindowMessages int64
	RecentMessages int64
	RecentWindow   time.Duration
	// Signal is the observation triple ClassifySignal needs to assign
	// a trust level. Zero value defaults to the most pessimistic
	// reading (MCP-pings only, low quality).
	Signal SignalInputs
	Now    time.Time
}

// ComputeSessionBudget returns a SessionBudget for the named plan from
// the supplied inputs. Plans without a published window cap return a
// "unknown" recommendation rather than fabricating one.
func ComputeSessionBudget(planName string, in SessionBudgetInputs) (SessionBudget, error) {
	p, ok := Lookup(planName)
	if !ok {
		return SessionBudget{}, errUnknownPlan(planName)
	}
	out := SessionBudget{
		PlanName:          planName,
		Display:           p.Display,
		Provider:          p.Provider,
		WindowCap:         p.MessagesPerWindow,
		WindowConsumed:    in.WindowMessages,
		WindowResetsIn:    p.RateLimitWindow.String(),
		windowResetsInDur: p.RateLimitWindow,
		Confidence:        ConfidenceLow,
		RecommendedAction: ActionUnknown,
		SignalQuality:     ClassifySignal(in.Signal),
	}
	if p.RateLimitWindow <= 0 || p.MessagesPerWindow <= 0 {
		out.Note = "plan publishes no concrete rate-limit cap; budget signal unavailable"
		return out, nil
	}
	out.WindowPct = math.Round(float64(in.WindowMessages)/float64(p.MessagesPerWindow)*10000) / 100
	out.HeadroomUntilCap = p.MessagesPerWindow - in.WindowMessages
	if out.HeadroomUntilCap < 0 {
		out.HeadroomUntilCap = 0
	}

	switch {
	case in.RecentWindow <= 0 || in.RecentMessages == 0:
		out.Confidence = ConfidenceLow
		if out.WindowPct >= 80 {
			out.RecommendedAction = ActionSlowDown
		} else {
			out.RecommendedAction = ActionContinue
		}
		out.Note = "no recent traffic to extrapolate burn rate"
		return out, nil
	default:
		out.RecentRatePerHour = math.Round(
			float64(in.RecentMessages)/in.RecentWindow.Hours()*10) / 10
		if in.RecentWindow >= 30*time.Minute && in.RecentMessages >= 5 {
			out.Confidence = ConfidenceHigh
		} else {
			out.Confidence = ConfidenceMedium
		}
		out.RecommendedAction = recommendAction(out.WindowPct, out.HeadroomUntilCap, out.RecentRatePerHour)
		if out.HeadroomUntilCap > 0 && out.RecentRatePerHour > 0 {
			hoursLeft := float64(out.HeadroomUntilCap) / out.RecentRatePerHour
			d := time.Duration(hoursLeft * float64(time.Hour))
			out.willHitCapWithinDur = d
			out.WillHitCapWithin = d.Round(time.Minute).String()
		} else if out.HeadroomUntilCap == 0 {
			out.WillHitCapWithin = "0s"
		}
	}
	return out, nil
}

// recommendAction maps the (consumed, headroom, burn rate) triple to
// the closed action set. Thresholds chosen to match the headroom
// classifier (80/60) so the two surfaces never disagree.
func recommendAction(pct float64, headroom int64, ratePerHour float64) string {
	switch {
	case headroom <= 0:
		return ActionWaitReset
	case pct >= 95:
		return ActionWaitReset
	case pct >= 80:
		if ratePerHour > 0 && float64(headroom)/ratePerHour < 1.0 {
			return ActionWaitReset
		}
		return ActionSlowDown
	case pct >= 60 && ratePerHour > 0 && float64(headroom)/ratePerHour < 1.5:
		return ActionSwitchModel
	default:
		return ActionContinue
	}
}

func errUnknownPlan(name string) error {
	return &unknownPlanError{name: name}
}

type unknownPlanError struct{ name string }

func (e *unknownPlanError) Error() string {
	return "unknown plan: " + e.name
}
