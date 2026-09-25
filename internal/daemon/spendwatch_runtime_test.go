package daemon

import (
	"io"
	"log/slog"
	"testing"

	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/infra/lifecycle"
)

func TestSpendWatcherRuntimeDoesNotRegisterWithoutAggregator(t *testing.T) {
	cfg := config.Default()
	cfg.Mode = "active"
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	sup := lifecycle.New(t.Context(), logger)
	startSpendWatcherRuntime(cfg, nil, nil, sup, logger)
	if got := sup.Running(); len(got) != 0 {
		t.Fatalf("runtime registered tasks without an analytics aggregator: %v", got)
	}
}
