package presentation

// ResourceSignal is a normalized, evidence-carrying view of one existing
// session-budget or plan-headroom result. It adds no quota calculation.
type ResourceSignal struct {
	Provider           string
	Display            string
	Basis              string
	RecommendedAction  string
	OverageRisk        string
	WindowPct          float64
	Confidence         string
	SignalQualityLevel string
	Caveat             string
}

// ResourceInsight is the compact headline for resource pressure. The raw
// reports remain available to inspect each provider and measurement.
type ResourceInsight struct {
	Level              string   `json:"level" jsonschema:"description=attention when an existing signal recommends intervention or reports medium/high risk, clear only when all available signals are non-alarming, uncertain when mixed with unknown signals, unavailable when no signal can assess capacity"`
	Summary            string   `json:"summary"`
	Provider           string   `json:"provider,omitempty"`
	Basis              string   `json:"basis,omitempty"`
	RecommendedAction  string   `json:"recommended_action,omitempty"`
	OverageRisk        string   `json:"overage_risk,omitempty"`
	WindowPct          *float64 `json:"window_pct,omitempty"`
	Confidence         string   `json:"confidence,omitempty"`
	SignalQualityLevel string   `json:"signal_quality_level,omitempty"`
	Caveat             string   `json:"caveat,omitempty"`
}

// ForResources selects the most decision-relevant existing signal. An
// unknown signal prevents a reassuring clear headline, but does not erase an
// independently observed warning from another resource.
func ForResources(signals []ResourceSignal) ResourceInsight {
	if len(signals) == 0 {
		return ResourceInsight{Level: "unavailable", Summary: "No configured resource signal is available; check plan setup and storage."}
	}

	selected, score := -1, 0
	unknown := false
	lowQuality := false
	for i, signal := range signals {
		current, known := resourceSignalScore(signal)
		if !known {
			unknown = true
			continue
		}
		if current < 2 && signal.SignalQualityLevel == "low" {
			lowQuality = true
		}
		if selected < 0 || current > score {
			selected, score = i, current
		}
	}
	if selected < 0 {
		return ResourceInsight{Level: "unavailable", Summary: "Configured capacity is not measurable enough for a recommendation; inspect signal quality and notes."}
	}

	s := signals[selected]
	out := ResourceInsight{
		Provider:           s.Provider,
		Basis:              s.Basis,
		RecommendedAction:  s.RecommendedAction,
		OverageRisk:        s.OverageRisk,
		Confidence:         s.Confidence,
		SignalQualityLevel: s.SignalQualityLevel,
		Caveat:             s.Caveat,
	}
	if s.Basis == "session_budget" {
		pct := s.WindowPct
		out.WindowPct = &pct
	}

	subject := s.Display
	if subject == "" {
		subject = s.Provider
	}
	switch {
	case score >= 2:
		out.Level = "attention"
		if s.RecommendedAction != "" && s.RecommendedAction != "continue" && s.RecommendedAction != "unknown" {
			out.Summary = subject + " session budget recommends " + s.RecommendedAction + "."
		} else {
			out.Summary = subject + " reports " + s.OverageRisk + " overage risk."
		}
	case unknown || lowQuality:
		out.Level = "uncertain"
		switch {
		case unknown && lowQuality:
			out.Summary = subject + " has no current intervention signal, but signal quality is low and some configured capacity is unknown."
		case lowQuality:
			out.Summary = subject + " has no current intervention signal, but its signal quality is low; absence is not evidence of spare capacity."
		default:
			out.Summary = subject + " has no current intervention signal, but some configured capacity is unknown."
		}
	default:
		out.Level = "clear"
		if s.Basis == "session_budget" {
			out.Summary = subject + " session budget currently recommends continuing."
		} else {
			out.Summary = subject + " currently reports low overage risk."
		}
	}
	return out
}

func resourceSignalScore(s ResourceSignal) (int, bool) {
	score, known := 0, false
	switch s.RecommendedAction {
	case "continue":
		known, score = true, 1
	case "slow_down":
		known, score = true, 2
	case "switch_model":
		known, score = true, 3
	case "wait_for_reset":
		known, score = true, 4
	}
	switch s.OverageRisk {
	case "low":
		known = true
		if score < 1 {
			score = 1
		}
	case "medium":
		known = true
		if score < 2 {
			score = 2
		}
	case "high":
		known = true
		if score < 3 {
			score = 3
		}
	}
	return score, known
}
