package state

import (
	"context"
	"time"

	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/contexts/observability/freshness"
	"go.klarlabs.de/tokenops/internal/infra/sourceprobe"
)

// SourceRegistry records each reader's polls as they happen, which is
// half of what says whether a source still works.
type SourceRegistry = freshness.Registry

// NewSourceRegistry is an empty registry for the readers to record into.
func NewSourceRegistry() *SourceRegistry { return freshness.NewRegistry() }

// SourceCounter is the slice of the event store the assessment reads.
type SourceCounter interface {
	CountBySource(ctx context.Context, since, until time.Time) (map[string]int64, error)
	LastEventBySource(ctx context.Context) (map[string]time.Time, error)
}

// AssessSources says, for each source cfg asks the daemon to observe,
// whether it is still working: what it ingested lately, when it last
// did, what its readers' polls reported, whether its local origin moved,
// and whether its reader stopped. stopped is the supervised readers that
// exited, keyed by source tag.
//
// A store read that fails reports the sources without counts rather than
// nothing at all: the poll records and origin probes still say something
// useful, and an empty list would read as "no sources configured".
func AssessSources(ctx context.Context, cfg config.Config, store SourceCounter, registry *SourceRegistry, stopped map[string]error, now time.Time) []SourceReport {
	counts, err := store.CountBySource(ctx, now.Add(-freshness.DefaultWindow), now)
	if err != nil {
		counts = nil
	}
	lastSeen, err := store.LastEventBySource(ctx)
	if err != nil {
		lastSeen = nil
	}
	var polls map[string]freshness.Poll
	if registry != nil {
		polls = registry.Polls()
	}
	return freshness.Assess(freshness.Inputs{
		Now:            now,
		Window:         freshness.DefaultWindow,
		Sources:        configuredSources(cfg),
		EventsInWindow: counts,
		LastEventAt:    lastSeen,
		OriginNewest:   originsOf(cfg),
		Polls:          polls,
		Stopped:        stopped,
	})
}

// configuredSources lists what the daemon was asked to observe.
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
