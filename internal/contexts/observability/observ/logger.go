// Package observ provides shared observability primitives for the daemon.
//
// This package currently exposes the structured logger used by the proxy and
// supporting subsystems; metrics and tracing helpers will be added by later
// tasks (otel-exporter, observability-platform).
package observ

import (
	"io"
	"log/slog"
	"strings"

	"github.com/felixgeelhaar/bolt"
)

// NewLogger returns a slog.Logger configured for the given level and format.
// JSON format uses bolt's zero-allocation slog handler; text format keeps
// the stdlib handler for human-readable dev output.
func NewLogger(w io.Writer, level, format string) *slog.Logger {
	lvl := parseLevel(level)
	var handler slog.Handler
	switch strings.ToLower(format) {
	case "json":
		handler = bolt.NewSlogHandler(w, &bolt.SlogHandlerOptions{Level: lvl})
	default:
		handler = slog.NewTextHandler(w, &slog.HandlerOptions{Level: lvl})
	}
	return slog.New(handler)
}

func parseLevel(level string) slog.Level {
	switch strings.ToLower(level) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
