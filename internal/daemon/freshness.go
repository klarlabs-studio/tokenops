package daemon

import (
	"context"
	"time"

	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/contexts/observability/freshness"
	"go.klarlabs.de/tokenops/internal/infra/sourceprobe"
)

// freshnessCounter is the slice of the event store the assessment needs.
// Declared here rather than taken as *sqlite.Store so the assembly is
// testable without a database.
type freshnessCounter interface {
	CountBySource(ctx context.Context, since, until time.Time) (map[string]int64, error)
	LastEventBySource(ctx context.Context) (map[string]time.Time, error)
}

// sourceFreshnessFn builds the callback GET /api/sources serves.
//
// The daemon is the only process that knows all three facts at once. It
// owns the event store, it constructs the pollers, and it can read the
// local origins — where `tokenops serve` (the MCP server) and a dashboard
// are different processes entirely. Exposing the assessment over the API
// is what lets them answer "is this still working" without each
// re-deriving it from a database they may not have.
//
// The callback is evaluated per request rather than cached: a status
// answer that is minutes old is the failure mode this whole phase is
// about.
func sourceFreshnessFn(cfg config.Config, store freshnessCounter, health *freshness.Registry) func() []freshness.Report {
	if store == nil {
		return nil
	}
	return func() []freshness.Report {
		// A status query must not hang behind a busy database. Answering
		// late is the same as not answering.
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		now := time.Now()
		counts, err := store.CountBySource(ctx, now.Add(-freshness.DefaultWindow), now)
		if err != nil {
			// Report the sources without counts rather than nothing at
			// all: the poll records and origin probes still say
			// something useful, and an empty list would read as "no
			// sources configured".
			counts = nil
		}
		lastSeen, err := store.LastEventBySource(ctx)
		if err != nil {
			lastSeen = nil
		}

		return freshness.Assess(freshness.Inputs{
			Now:            now,
			Window:         freshness.DefaultWindow,
			Sources:        configuredSources(cfg),
			EventsInWindow: counts,
			LastEventAt:    lastSeen,
			OriginNewest:   originsOf(cfg),
			Polls:          health.Polls(),
		})
	}
}

// configuredSources lists what this daemon was asked to observe.
//
// AlwaysOn sources are included. The cursor turn poller has no config
// block — it reads a ledger the coach hook writes — and that is exactly
// why it appeared in no registry, was never staleness-checked, and was
// invisible to `vendor-usage status`.
func configuredSources(cfg config.Config) []freshness.Source {
	all := cfg.VendorUsageSources()
	out := make([]freshness.Source, 0, len(all))
	for _, s := range all {
		if !s.Enabled && !s.AlwaysOn {
			continue
		}
		out = append(out, freshness.Source{Name: s.Name, Tag: s.SourceTag})
	}
	return out
}

// originsOf reads each local reader's upstream, so "the operator has not
// used this vendor" stays distinguishable from "the reader died".
func originsOf(cfg config.Config) map[string]freshness.Origin {
	probes := sourceprobe.All(cfg)
	if len(probes) == 0 {
		return nil
	}
	out := make(map[string]freshness.Origin, len(probes))
	for tag, probe := range probes {
		if probe == nil {
			continue
		}
		at, known := probe()
		out[tag] = freshness.Origin{At: at, Known: known}
	}
	return out
}
