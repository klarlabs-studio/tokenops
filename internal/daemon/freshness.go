package daemon

import (
	"context"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/state"
	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/infra/lifecycle"
)

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
func sourceFreshnessFn(cfg config.Config, store state.SourceCounter, health *state.SourceRegistry, sup *lifecycle.Supervisor) func() []state.SourceReport {
	if store == nil {
		return nil
	}
	return func() []state.SourceReport {
		// A status query must not hang behind a busy database. Answering
		// late is the same as not answering.
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return state.AssessSources(ctx, cfg, store, health, stoppedReaders(sup), time.Now())
	}
}

// stoppedReaders reports which supervised readers have exited. A reader
// that exited is the strongest signal there is, and it only became
// knowable once the pollers ran under a supervisor. The task names are the
// source tags on purpose, so a supervisor failure lines up with the source
// it belongs to without a translation table.
//
// Nothing could answer this before: the daemon started nine pollers with
// a bare `go func()`, so one dying logged a warning and vanished while
// every status surface went on reporting the daemon healthy.
func stoppedReaders(sup *lifecycle.Supervisor) map[string]error {
	if sup == nil {
		return nil
	}
	return sup.Failed()
}
