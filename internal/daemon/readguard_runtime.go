package daemon

import (
	"context"
	"log/slog"
	"time"

	"go.klarlabs.de/tokenops/internal/events"
	"go.klarlabs.de/tokenops/internal/infra/lifecycle"
)

// startReadGuardRuntime owns the recurring read-guard ledger ingestion task.
func startReadGuardRuntime(bus events.Bus, sup *lifecycle.Supervisor, logger *slog.Logger) {
	if bus == nil {
		return
	}
	sup.Go("read-guard-ingest", func(taskCtx context.Context) error {
		runReadGuardIngest(taskCtx, bus, logger, 2*time.Minute, "")
		return nil
	})
	logger.Info("read-guard reclamation ingest live")
}
