package scorecard

import (
	"context"
	"os"
	"path/filepath"
	"time"

	"github.com/felixgeelhaar/tokenops/internal/storage/sqlite"
	"github.com/felixgeelhaar/tokenops/pkg/eventschema"
)

// sqliteReader adapts *sqlite.Store to the EventReader port. Kept inside
// the scorecard package so the domain Compute function never imports
// sqlite directly — only the adapter does.
type sqliteReader struct{ store *sqlite.Store }

func (a sqliteReader) ReadEvents(ctx context.Context, t eventschema.EventType, since time.Time) ([]*eventschema.Envelope, error) {
	return a.store.Query(ctx, sqlite.Filter{Type: t, Since: since, Limit: 100_000})
}

// Defaults used when neither the live event store nor operator overrides
// supply a KPI value. Kept in one place so both CLI and MCP adapters
// share the same fallback semantics.
const (
	DefaultFVTSeconds = 45.0
	DefaultTEUPct     = 15.0
	DefaultSACPct     = 80.0
)

// BuildParams bundles every input the adapters supply when constructing
// the wedge scorecard. CLI flags and MCP arguments populate the same
// fields so the application logic — store open, live compute, override
// merge — lives in this package.
type BuildParams struct {
	// DBPath is the events.db location. Empty defaults to
	// $HOME/.tokenops/events.db.
	DBPath string
	// SinceDays bounds the live compute window. Zero defaults to 7.
	SinceDays int
	// Overrides, when non-zero, replace the corresponding live KPI value.
	FVTSecondsOverride float64
	TEUPctOverride     float64
	SACPctOverride     float64
	// BaselineRef is the operator-supplied baseline identifier carried
	// through to Scorecard.BaselineRef.
	BaselineRef string
	// ClockNow allows tests to inject a deterministic clock. Defaults to
	// time.Now when nil.
	ClockNow func() time.Time
}

// Build produces a Scorecard by:
//  1. resolving the events.db path (DBPath → ~/.tokenops/events.db),
//  2. computing live KPIs when the store exists,
//  3. falling through to package defaults when the live store is empty,
//  4. applying any operator overrides.
//
// Errors during store open / compute are non-fatal: the function falls
// back to defaults and reports them implicitly via the Scorecard grades.
// This matches the CLI behavior on a fresh install (no daemon history).
func Build(ctx context.Context, params BuildParams) *Scorecard {
	if params.SinceDays == 0 {
		params.SinceDays = 7
	}
	if params.ClockNow == nil {
		params.ClockNow = time.Now
	}
	fvt, teu, sac := DefaultFVTSeconds, DefaultTEUPct, DefaultSACPct
	var anyComputed bool

	dbPath := params.DBPath
	if dbPath == "" {
		if home, err := os.UserHomeDir(); err == nil {
			dbPath = filepath.Join(home, ".tokenops", "events.db")
		}
	}
	if dbPath != "" {
		if _, err := os.Stat(dbPath); err == nil {
			storeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			defer cancel()
			if store, err := sqlite.Open(storeCtx, dbPath, sqlite.Options{}); err == nil {
				defer func() { _ = store.Close() }()
				since := params.ClockNow().Add(-time.Duration(params.SinceDays) * 24 * time.Hour)
				if kpis, err := Compute(storeCtx, sqliteReader{store: store}, since); err == nil {
					if kpis.FVTComputed {
						fvt = kpis.FVTSeconds
						anyComputed = true
					}
					if kpis.TEUComputed {
						teu = kpis.TokenEfficiency
						anyComputed = true
					}
					if kpis.SACComputed {
						sac = kpis.SpendAttribution
						anyComputed = true
					}
				}
			}
		}
	}
	if params.FVTSecondsOverride > 0 {
		fvt = params.FVTSecondsOverride
		anyComputed = true
	}
	if params.TEUPctOverride > 0 {
		teu = params.TEUPctOverride
		anyComputed = true
	}
	if params.SACPctOverride > 0 {
		sac = params.SACPctOverride
		anyComputed = true
	}
	if !anyComputed {
		return NewWarmingUp(params.BaselineRef)
	}
	return New(fvt, teu, sac, params.BaselineRef)
}

// BuildFromStore is the variant adapters use when they already hold an
// open *sqlite.Store and want to skip the path-resolution dance (the MCP
// daemon does this, since the store opens at startup). Overrides + clock
// behave the same as Build.
func BuildFromStore(ctx context.Context, store *sqlite.Store, params BuildParams) *Scorecard {
	if params.SinceDays == 0 {
		params.SinceDays = 7
	}
	if params.ClockNow == nil {
		params.ClockNow = time.Now
	}
	fvt, teu, sac := DefaultFVTSeconds, DefaultTEUPct, DefaultSACPct
	var anyComputed bool
	if store != nil {
		since := params.ClockNow().Add(-time.Duration(params.SinceDays) * 24 * time.Hour)
		if kpis, err := Compute(ctx, sqliteReader{store: store}, since); err == nil {
			if kpis.FVTComputed {
				fvt = kpis.FVTSeconds
				anyComputed = true
			}
			if kpis.TEUComputed {
				teu = kpis.TokenEfficiency
				anyComputed = true
			}
			if kpis.SACComputed {
				sac = kpis.SpendAttribution
				anyComputed = true
			}
		}
	}
	if params.FVTSecondsOverride > 0 {
		fvt = params.FVTSecondsOverride
		anyComputed = true
	}
	if params.TEUPctOverride > 0 {
		teu = params.TEUPctOverride
		anyComputed = true
	}
	if params.SACPctOverride > 0 {
		sac = params.SACPctOverride
		anyComputed = true
	}
	if !anyComputed {
		return NewWarmingUp(params.BaselineRef)
	}
	return New(fvt, teu, sac, params.BaselineRef)
}
