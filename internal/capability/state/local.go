package state

import (
	"context"
	"fmt"
	"time"

	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/infra/sourceprobe"
)

// StaleSources is every enabled source that has gone quiet over the
// staleness window, probed the same way on every surface. A store error is
// "nothing known", never a failure: a health check must not fail on the
// thing it is checking.
func StaleSources(ctx context.Context, cfg config.Config, counter config.SourceCounter, now time.Time) []config.StaleSource {
	stale, err := cfg.CheckStaleIngestion(ctx, counter, sourceprobe.All(cfg), config.StaleIngestionWindow, now)
	if err != nil {
		return nil
	}
	return stale
}

// LocalWarnings is what the event store says about ingestion, for a
// surface that reads it directly rather than through a daemon: one
// warning per quiet source, then one per retention rule that names a
// source nothing has written. Nil when there is nothing to report.
//
// A retention rule naming a source that has never written an event is
// doing nothing, silently. The tag is not always what the operator sees —
// Cursor's ledger lives in ~/.tokenops/cursor-turns while its events are
// stamped cursor-hook — so a plausible-looking key can pin nothing at all.
func LocalWarnings(ctx context.Context, cfg config.Config, counter config.SourceCounter, now time.Time) []string {
	warnings := StaleWarnings(StaleSources(ctx, cfg, counter, now))
	counts, err := counter.CountBySource(ctx, time.Time{}, time.Time{})
	if err != nil {
		return warnings
	}
	for _, key := range cfg.UnmatchedRetentionSources(counts) {
		warnings = append(warnings, fmt.Sprintf(
			"retention.keep_by_source[%q] matches no source that has produced events — "+
				"the rule is doing nothing; check the tag with `tokenops vendor-usage status`", key))
	}
	return warnings
}

// OfDaemon grades a daemon from outside: readyz is the status its /readyz
// answered, warnings whether this surface found anything that reduces
// coverage. The daemon's explicit readiness is trusted; a missing or
// unrecognised answer stays not_ready rather than being inferred from an
// HTTP 200.
func OfDaemon(readyz string, warnings bool) string {
	switch readyz {
	case Ready:
		if warnings {
			return Degraded
		}
		return Ready
	case NotConfigured:
		return NotConfigured
	default:
		return NotReady
	}
}
