package daemon

import (
	"io"
	"log/slog"
	"testing"

	"go.klarlabs.de/tokenops/internal/infra/lifecycle"
)

func TestReadGuardRuntimeDoesNotRegisterWithoutBus(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	sup := lifecycle.New(t.Context(), logger)
	startReadGuardRuntime(nil, sup, logger)
	if got := sup.Running(); len(got) != 0 {
		t.Fatalf("runtime registered tasks without an event bus: %v", got)
	}
}
