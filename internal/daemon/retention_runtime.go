package daemon

import (
	"context"
	"log/slog"

	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/contexts/telemetry/retention"
	"go.klarlabs.de/tokenops/internal/infra/lifecycle"
	"go.klarlabs.de/tokenops/internal/storage/sqlite"
)

// startRetentionRuntime creates and supervises the configured event-retention
// worker. The wrapper waits for the scheduler's internal loop to stop, making
// its shutdown part of the daemon lifecycle.
func startRetentionRuntime(cfg config.RetentionConfig, store *sqlite.Store, sup *lifecycle.Supervisor, logger *slog.Logger) error {
	if !cfg.Enabled() {
		return nil
	}
	policies, err := retentionPolicies(cfg)
	if err != nil {
		return err
	}
	if len(policies) == 0 {
		return nil
	}
	scheduler := retention.NewScheduler(retention.New(store, retentionConfig(cfg, policies, logger)))
	sup.Go("telemetry-retention", func(ctx context.Context) error {
		scheduler.Start(ctx)
		scheduler.Wait()
		return nil
	})
	logger.Info("retention scheduler live",
		"policies", len(policies), "reclaim", cfg.Reclaim,
		"first_pass_in", retentionStartDelay)
	return nil
}
