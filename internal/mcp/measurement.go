package mcp

import (
	"fmt"

	"go.klarlabs.de/tokenops/internal/config"
)

// measurementQuality describes how much a spend figure can be trusted, or nil
// when ingestion is healthy and the number means what it says.
//
// A total of zero has two very different causes: nothing was spent, or nothing
// was measured. They were formatted identically, so during a 27-day ingestion
// outage `spend_summary` answered "$0.00, 700 tokens, 1 request" for a period
// containing thousands of requests — a shape indistinguishable from a quiet
// fortnight, and confident about it.
//
// Reported alongside the figure rather than instead of it: the number is still
// the best available answer, it is simply a lower bound rather than a
// measurement. Refusing outright would break every caller that reads these
// tools on a schedule, and a caveat they can branch on is more useful than an
// error they will retry.
func measurementQuality(d Deps) map[string]any {
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

	return map[string]any{
		"trusted":       false,
		"severity":      worst.Severity(),
		"stale_sources": len(stale),
		"reason":        worst.Warning(),
		"note": fmt.Sprintf(
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
