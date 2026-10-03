package state

import (
	"context"
	"time"

	coachcap "go.klarlabs.de/tokenops/internal/capability/coach"
	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/contexts/observability/freshness"
)

// Deps is what a surface supplies to answer from the daemon's own process.
type Deps struct {
	// Config is the configuration the daemon runs with. nil means none
	// loaded.
	Config *config.Config
	// Count counts stored events per source; nil means no store.
	Count Counter
	// Health is each reader's ingestion health; nil when unknown.
	Health func() []freshness.Report
	// Ready is the daemon's readiness.
	Ready func() bool
	// Dropped is how many events the store failed to persist.
	Dropped func() int64
	// Stale lists enabled sources that have gone quiet.
	Stale func(ctx context.Context, now time.Time) []config.StaleSource
	// Coach reports the coach's dials and each power's autonomy.
	Coach func(now time.Time) coachcap.Report
}

// StatusOf is the daemon's own status.
func StatusOf(ctx context.Context, d Deps, now time.Time) Status {
	in := StatusInputs{Config: d.Config}
	if d.Ready != nil {
		in.Ready = d.Ready()
	}
	if d.Dropped != nil {
		in.Dropped = d.Dropped()
	}
	if d.Stale != nil {
		in.Stale = StaleWarnings(d.Stale(ctx, now))
	}
	return ComputeStatus(in)
}
