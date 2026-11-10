package daemon

import (
	"context"
	"log/slog"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/attribution"
	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/infra/routehistory"
	"go.klarlabs.de/tokenops/internal/storage/sqlite"
)

// correctAttribution runs the ADR 0009 corrections at start: Claude Code
// turns a gateway served, Codex turns on a custom model_provider, opencode
// turns under a raw provider ID, and usage a spend report recorded as
// covered by a plan. Each logs what it moved; a
// failure is logged and never stops the daemon, and the next start
// tries again.
func correctAttribution(ctx context.Context, cfg config.Config, store *sqlite.Store, routes *routehistory.Tracker, logger *slog.Logger) {
	if store == nil {
		return
	}
	logCorrections := func(harness string, cs []attribution.Correction, err error) {
		for _, c := range cs {
			logger.Info("re-attributed recorded usage", "harness", c.Harness, "from", c.From, "to", c.To,
				"model", c.Model, "endpoint", c.Endpoint, "calls", c.Calls, "reason", c.Reason)
		}
		if err != nil {
			logger.Warn("attribution correction failed; will retry at next start", "harness", harness, "err", err)
		}
	}
	cs, err := attribution.CorrectGateway(ctx, cfg, store, routes, time.Now().UTC())
	logCorrections("claude-code", cs, err)
	cs, err = attribution.CorrectCodex(ctx, cfg, store)
	logCorrections("codex", cs, err)
	cs, err = attribution.CorrectOpencode(ctx, store)
	logCorrections("opencode", cs, err)

	uncovered, err := attribution.CorrectSpendReports(ctx, store)
	for _, u := range uncovered {
		logger.Info("re-marked usage a spend report recorded as covered by a plan as billed",
			"source", u.Source, "provider", u.Provider, "events", u.Events, "reason", u.Reason)
	}
	if err != nil {
		logger.Warn("spend report correction failed; will retry at next start", "err", err)
	}
}
