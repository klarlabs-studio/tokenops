package mcp

import (
	"fmt"

	"go.klarlabs.de/tokenops/internal/config"
)

// MeasurementWarning states that a spend figure cannot be trusted because
// ingestion has stopped, and how badly.
//
// It sits beside DataWarning, which answers a different question — that one
// says "most of this is synthetic", this one says "much of it was never
// recorded at all". A total of zero has two causes: nothing was spent, or
// nothing was measured. They were formatted identically, so during a 27-day
// ingestion outage tokenops_spend_summary answered "$0.00, 700 tokens, 1
// request" for a period containing thousands of requests — confidently, and in
// the same shape as a quiet fortnight.
type MeasurementWarning struct {
	// Trusted is always false; the field exists so a caller branching on
	// JSON does not have to infer meaning from the block's presence.
	Trusted bool `json:"trusted"`
	// Severity is the worst stale source's grade: warning, degraded, critical.
	Severity string `json:"severity"`
	// StaleSources counts how many enabled sources have stopped ingesting.
	StaleSources int `json:"stale_sources"`
	// Reason is the operator-facing warning for the worst source.
	Reason string `json:"reason"`
	// Hint says what the number now means.
	Hint string `json:"hint"`
}

// measurementQuality describes how far a spend figure can be trusted, or nil
// when ingestion is healthy and the number means what it says.
//
// Reported alongside the figure rather than instead of it: the number is still
// the best available answer, it is simply a lower bound. Refusing outright
// would break every caller reading these tools on a schedule, and a caveat
// they can branch on is more useful than an error they will retry.
func measurementQuality(d Deps) *MeasurementWarning {
	if d.StaleSources == nil {
		return nil
	}
	stale := d.StaleSources()
	if len(stale) == 0 {
		return nil
	}

	worst := stale[0]
	for _, s := range stale[1:] {
		if s.SilentFor > worst.SilentFor {
			worst = s
		}
	}

	return &MeasurementWarning{
		Trusted:      false,
		Severity:     worst.Severity(),
		StaleSources: len(stale),
		Reason:       worst.Warning(),
		Hint: fmt.Sprintf(
			"%s has not ingested for %s, so this figure covers only what reached the store: treat it as a lower bound, not a measurement of zero.",
			worst.SourceTag, staleFor(worst)),
	}
}

// staleFor renders the gap, or names the never-ingested case.
func staleFor(s config.StaleSource) string {
	if s.SilentFor <= 0 {
		return "any recorded period"
	}
	if days := int(s.SilentFor.Hours() / 24); days >= 1 {
		return fmt.Sprintf("%d days", days)
	}
	return fmt.Sprintf("%dh", int(s.SilentFor.Hours()))
}
