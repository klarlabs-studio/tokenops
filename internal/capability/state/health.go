package state

import (
	"fmt"
	"time"

	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/contexts/observability/freshness"
)

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

// FailingSourceNextAction is the remedy for a reader that is refused: the
// vendor turned its sign-in or key away, which no restart fixes.
const FailingSourceNextAction = "run 'tokenops vendor-usage status' to see which sign-in or key a source is refused with, then reconnect that provider with 'tokenops vendor-usage setup'"

// Warnings are the source warnings, split by remedy.
type Warnings struct {
	// Stale is the quiet sources: data was there to read, or nothing
	// could confirm otherwise, and nothing was read.
	Stale []string
	// Failing is the refused and stopped readers.
	Failing []string
}

// SourceWarnings words each source that needs attention by what is wrong
// with it. Healthy and idle sources say nothing: a vendor the operator is
// not using, or has not connected, is no fault.
func SourceWarnings(reports []SourceReport) Warnings {
	var out Warnings
	for _, r := range reports {
		switch {
		case r.Healthy():
		case r.ReaderStopped:
			out.Failing = append(out.Failing, fmt.Sprintf(
				"ingestion failing [%s]: %s's reader stopped (%s) — it does not come back on its own; %s",
				r.Severity, r.Tag, r.LastError, config.DaemonRunRemedy))
		case r.State == freshness.StateFailing:
			out.Failing = append(out.Failing, fmt.Sprintf(
				"ingestion failing [%s]: %s is being refused (%s) — run 'tokenops vendor-usage status' to see its sign-in or key",
				r.Severity, r.Tag, r.LastError))
		default:
			out.Stale = append(out.Stale, config.StaleSource{
				Name:        r.Name,
				SourceTag:   r.Tag,
				WindowHours: int(r.Window / time.Hour),
				SilentFor:   r.SilentFor,
			}.Warning())
		}
	}
	return out
}
