package bootstrap

import (
	"io"
	"log/slog"

	"go.klarlabs.de/tokenops/internal/contexts/observability/observ"
)

// EventCounter counts each kind of domain event the daemon has seen,
// replayed history included; Components holds the daemon's.
type EventCounter = observ.EventCounter

// NewLogger is the daemon's structured logger at level ("debug", "info",
// …) in format ("json" or text).
func NewLogger(w io.Writer, level, format string) *slog.Logger {
	return observ.NewLogger(w, level, format)
}
