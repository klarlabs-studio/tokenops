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
	// Authoritative carries the vendor's OWN reported quota for the
	// window when a snapshot source is available (Anthropic cookie
	// five_hour/seven_day %, Codex rate_limits primary/secondary %,
	// Copilot quota). When present it overrides the message-count
	// heuristic — WindowPct comes straight from the vendor's meter and,
	// when the vendor reports it, ResetsIn is exact. This is the
	// difference between guessing from event counts and reading the
	// actual limit.
	Authoritative *AuthoritativeWindow
}

// AuthoritativeWindow is a vendor-reported rate-limit snapshot: the
// share of the window already consumed and (when known) the time until
// it resets. Source labels which meter it came from, for the caveat.
type AuthoritativeWindow struct {
	UsedPct  float64
	ResetsIn time.Duration
	Source   string
}

// ComputeSessionBudget returns a SessionBudget for the named plan from
// the supplied inputs. Plans without a published window cap return a
// "unknown" recommendation rather than fabricating one.
func ComputeSessionBudget(planName string, in SessionBudgetInputs) (SessionBudget, error) {
	p, ok := Lookup(planName)
	if !ok {
		return SessionBudget{}, errUnknownPlan(planName)
	}
	// Authoritative vendor meter wins outright: no reason to extrapolate
	// from event counts when the vendor tells us the exact % used. This
	// also serves plans that publish a window but no message cap (e.g.
	// Claude Code Max/Pro), which the message-count path cannot score.
	if in.Authoritative != nil {
		return computeFromAuthoritative(planName, p, in), nil
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

// computeFromAuthoritative builds a SessionBudget straight from the
// vendor's reported window %, bypassing the message-count extrapolation.
// Confidence is high because the number is the vendor's own meter, not an
// inference. Works even when the plan has no message cap (the % and reset
// are all that is needed to advise the agent).
func computeFromAuthoritative(planName string, p Plan, in SessionBudgetInputs) SessionBudget {
	a := in.Authoritative
	pct := a.UsedPct
	if pct < 0 {
		pct = 0
	}
	if pct > 100 {
		pct = 100
	}
	out := SessionBudget{
		PlanName:          planName,
		Display:           p.Display,
		Provider:          p.Provider,
		WindowCap:         p.MessagesPerWindow,
		WindowPct:         math.Round(pct*100) / 100,
		Confidence:        ConfidenceHigh,
		RecommendedAction: recommendActionByPct(pct),
		SignalQuality:     ClassifySignal(in.Signal),
		Note:              "window % is the vendor's reported quota meter (" + a.Source + "), not a message-count estimate",
	}
	if a.ResetsIn > 0 {
		out.windowResetsInDur = a.ResetsIn
		out.WindowResetsIn = a.ResetsIn.Round(time.Minute).String()
	} else {
		out.windowResetsInDur = p.RateLimitWindow
		out.WindowResetsIn = p.RateLimitWindow.String()
	}
	// Message headroom is only meaningful when the plan publishes a cap;
	// otherwise the % + reset carry the whole signal.
	if p.MessagesPerWindow > 0 {
		out.HeadroomUntilCap = int64(math.Round(float64(p.MessagesPerWindow) * (100 - pct) / 100))
		if out.HeadroomUntilCap < 0 {
			out.HeadroomUntilCap = 0
		}
		out.WindowConsumed = p.MessagesPerWindow - out.HeadroomUntilCap
	}
	return out
}

// recommendActionByPct maps a vendor-reported window % to the action set
// without a burn-rate estimate (the authoritative path may not carry one).
func recommendActionByPct(pct float64) string {
	switch {
	case pct >= 95:
		return ActionWaitReset
	case pct >= 80:
		return ActionSlowDown
	case pct >= 60:
		return ActionSwitchModel
	default:
		return ActionContinue
	}
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
