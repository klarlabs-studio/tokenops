package daemon

import (
	"io"
	"log/slog"
	"testing"

	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/contexts/optimization/optimizer/router"
	"go.klarlabs.de/tokenops/internal/infra/lifecycle"
)

func TestWindowPressureRuntimeDoesNotRegisterWithoutStore(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	sup := lifecycle.New(t.Context(), logger)
	rc := &router.Config{}
	if got := startWindowPressureRuntime(config.Default(), rc, nil, sup, logger); got != nil {
		t.Fatal("runtime returned a probe without an event store")
	}
	if rc.WindowPressure != nil {
		t.Fatal("runtime installed a window-pressure reader without an event store")
	}
	if got := sup.Running(); len(got) != 0 {
		t.Fatalf("runtime registered tasks without an event store: %v", got)
	}
}
