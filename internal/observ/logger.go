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
)

// NewLogger returns a slog.Logger configured for the given level and format.
// Unknown levels fall back to info; unknown formats fall back to text.
func NewLogger(w io.Writer, level, format string) *slog.Logger {
	opts := &slog.HandlerOptions{Level: parseLevel(level)}

	var handler slog.Handler
	switch strings.ToLower(format) {
	case "json":
		handler = slog.NewJSONHandler(w, opts)
	default:
		handler = slog.NewTextHandler(w, opts)
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
