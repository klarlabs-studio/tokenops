package state

import "go.klarlabs.de/tokenops/internal/contexts/observability/freshness"

// SourceReport is one reader's ingestion health.
//
// Aliased rather than copied so freshness stays the single definition,
// while adapters name the capability's type and do not reach past it
// (ADR 0010).
type SourceReport = freshness.Report

// SourceHealth is the answer to "which of my sources is still working":
// every configured source, healthy ones included, and how many need
// attention.
type SourceHealth struct {
	// Sources is every configured source, healthy ones included. A
	// caller that wants only the problems can filter; a caller that
	// wants to show an operator what is being watched could not
	// previously do so at all.
	Sources []SourceReport `json:"sources"`
	// Unhealthy is how many need attention, so a caller can decide
	// whether to render anything without walking the list.
	Unhealthy int `json:"unhealthy"`
}

// SourceHealthOf counts the unhealthy reports. A nil list becomes an
// empty one, so a caller iterating Sources need not special-case the
// difference between null and [].
func SourceHealthOf(reports []SourceReport) SourceHealth {
	out := SourceHealth{Sources: reports}
	for _, r := range reports {
		if !r.Healthy() {
			out.Unhealthy++
		}
	}
	if out.Sources == nil {
		out.Sources = []SourceReport{}
	}
	return out
}
