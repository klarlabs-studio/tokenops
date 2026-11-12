package daemon

import (
	"context"
	"log/slog"

	"go.klarlabs.de/tokenops/internal/capability/attribution"
	"go.klarlabs.de/tokenops/internal/storage/sqlite"
)

// correctCodexOutput runs attribution.CorrectCodexOutput at start and
// logs what it corrected. A failure or shutdown is logged and never stops
// the daemon; the next start carries on where it stopped.
func correctCodexOutput(ctx context.Context, store *sqlite.Store, logger *slog.Logger) {
	if store == nil {
		return
	}
	n, err := attribution.CorrectCodexOutput(ctx, store)
	if n > 0 {
		logger.Info("corrected recorded Codex output", "calls", n, "reason", attribution.CodexOutputReason)
	}
	if err != nil {
		logger.Warn("Codex output correction failed; will retry at next start", "err", err)
	}
}
