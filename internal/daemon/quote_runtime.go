package daemon

import (
	"context"
	"log/slog"
	"time"

	"go.klarlabs.de/tokenops/internal/bootstrap"
	"go.klarlabs.de/tokenops/internal/capability/workquote"
	"go.klarlabs.de/tokenops/internal/infra/lifecycle"
)

// The quote's basis reads a month of transcripts and turns, so it runs in
// the background: a few minutes after start, once ingestion has caught
// up, then checks every few hours whether the event log has moved, and
// computes again only when it has (ADR 0019).
const (
	quoteBasisDelay = 12 * time.Minute
	quoteBasisEvery = 3 * time.Hour
)

func startQuoteBasisRuntime(c *bootstrap.Components, sup *lifecycle.Supervisor, logger *slog.Logger) {
	if c == nil || c.Aggregator == nil || c.Store == nil {
		return
	}
	in := workquote.InputsFor(c.Aggregator, c.Store)
	sup.Go("quote-basis", func(ctx context.Context) error {
		runQuoteBasis(ctx, c.Store, in, workquote.DefaultDir(), quoteBasisDelay, quoteBasisEvery, logger)
		return nil
	})
}

func runQuoteBasis(ctx context.Context, log workquote.Log, in workquote.Inputs, dir string, delay, every time.Duration, logger *slog.Logger) {
	wait := time.NewTimer(delay)
	defer wait.Stop()
	select {
	case <-ctx.Done():
		return
	case <-wait.C:
	}
	refresh := func() {
		start := time.Now()
		b, did, err := workquote.Refresh(ctx, log, in, dir, start.UTC())
		switch {
		case err != nil:
			logger.Warn("quote basis not stored", "err", err)
		case did:
			logger.Info("quote basis stored", "took", time.Since(start).Round(time.Millisecond),
				"instructions", len(b.Samples), "window_rates", len(b.Rates))
		}
	}
	refresh()
	tick := time.NewTicker(every)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			refresh()
		}
	}
}
