package daemon

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/contexts/spend/pricing"
	"go.klarlabs.de/tokenops/internal/contexts/spend/spend"
)

// The rate card goes stale on its own. A model released after the binary
// was built prices at zero, and a session that cost real money reports as
// free — which is the exact failure this tool exists to find, so leaving
// it to whoever remembers `tokenops pricing refresh` is not good enough.
//
// Three things about this loop matter more than the fetch itself.
//
// It applies what it fetches. A refresh that writes a snapshot the
// running daemon never reads is a no-op wearing the clothes of an update:
// the spend engine is built once at startup, so the new cards are handed
// to Engine.Replace rather than left on disk for the next restart.
//
// It does not write when nothing changed. Rate cards move on the order of
// weeks; a daily unconditional write would leave a year of near-identical
// snapshots and make `pricing diff` useless. A separate marker records
// that a check happened, so skipping the write does not cause a re-fetch
// on every restart.
//
// And it fails quiet and soft. No network, a bad payload, a source
// outage: log it and keep the card already in force. Pricing must never
// be the reason ingestion stops.

// lastCheckedFile records when the source was last consulted, separately
// from the snapshots, so a no-change check still counts as a check.
const lastCheckedFile = "last-checked"

// runPricingRefresh keeps the daemon's rate card current until ctx ends.
func runPricingRefresh(
	ctx context.Context,
	cfg config.Config,
	eng *spend.Engine,
	logger *slog.Logger,
) {
	rc := cfg.Pricing.Refresh
	if !rc.Enabled() || eng == nil {
		return
	}
	dir := pricing.ResolveDir("")
	every := rc.Every()

	// Only reach out at startup if the card is actually stale. A daemon
	// that restarts often should not re-fetch every time.
	if since := sinceLastCheck(dir); since < every {
		logger.Debug("pricing refresh not due", "last_checked_ago", since.Round(time.Minute), "interval", every)
	} else {
		refreshOnce(ctx, cfg, dir, eng, logger)
	}

	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			refreshOnce(ctx, cfg, dir, eng, logger)
		}
	}
}

// refreshOnce fetches, and applies the result only when it differs.
func refreshOnce(
	ctx context.Context,
	cfg config.Config,
	dir string,
	eng *spend.Engine,
	logger *slog.Logger,
) {
	src := pricing.SourceByName("litellm", "")
	if src == nil {
		return
	}
	fetchCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	snap, err := src.Fetch(fetchCtx)
	if err != nil {
		// Offline is the common case, not an incident. The card already
		// in force keeps pricing.
		logger.Warn("pricing refresh failed; keeping the current rate card", "err", err)
		return
	}
	markChecked(dir)

	// The guard warns, never blocks — same contract as the CLI. An
	// anomaly is a reason to look, not a reason to price nothing.
	if anomalies := pricing.Check(snap); len(anomalies) > 0 {
		logger.Warn("pricing consistency guard flagged the fetched rates",
			"anomalies", len(anomalies), "first", anomalies[0].String())
	}

	prev, _ := pricing.LatestSnapshot(dir)
	changes := pricing.Diff(prev, snap)
	if len(changes) == 0 {
		logger.Debug("pricing refresh: no change", "rates", len(snap.Rates))
		return
	}
	path, err := pricing.SaveSnapshot(dir, snap)
	if err != nil {
		logger.Warn("pricing refresh: snapshot not written", "err", err)
		return
	}

	// Apply it to the running engine. Without this the daemon would price
	// from the card it started with until someone restarted it.
	eng.Replace(pricing.EffectiveTables(dir, overridesFor(cfg)))
	logger.Info("pricing refreshed",
		"changes", len(changes), "rates", len(snap.Rates),
		"snapshot", filepath.Base(path), "first_change", pricing.FormatChange(changes[0]))
}

// overridesFor reloads the operator's negotiated rates so a refresh
// cannot drop them. They outrank every fetched card by construction.
func overridesFor(cfg config.Config) spend.Table {
	if cfg.Pricing.Path == "" {
		return spend.Table{}
	}
	ov, err := spend.LoadTableFile(cfg.Pricing.Path)
	if err != nil {
		return spend.Table{}
	}
	return ov
}

func sinceLastCheck(dir string) time.Duration {
	b, err := os.ReadFile(filepath.Join(dir, lastCheckedFile)) //nolint:gosec // state file in our own dir
	if err != nil {
		return time.Duration(1<<62 - 1) // never checked: due now
	}
	t, err := time.Parse(time.RFC3339, strings.TrimSpace(string(b)))
	if err != nil {
		return time.Duration(1<<62 - 1)
	}
	return time.Since(t)
}

func markChecked(dir string) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	_ = os.WriteFile(filepath.Join(dir, lastCheckedFile),
		[]byte(time.Now().UTC().Format(time.RFC3339)+"\n"), 0o644) //nolint:gosec // not a secret
}
