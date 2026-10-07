// Package daemon hosts the boot sequence shared by tokenopsd and the
// tokenops CLI start subcommand. It composes config, logger, proxy server,
// and graceful shutdown so callers do not duplicate lifecycle wiring.
package daemon

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/contexts/observability/observ"
)

// Run boots the daemon with cfg and blocks until ctx is cancelled (e.g. by
// SIGINT/SIGTERM). The logger is built from cfg.Log; pass logWriter=nil to
// emit to os.Stderr.
func Run(ctx context.Context, cfg config.Config, logWriter io.Writer) error {
	if logWriter == nil {
		logWriter = os.Stderr
	}
	logger := observ.NewLogger(logWriter, cfg.Log.Level, cfg.Log.Format)
	return RunWithLogger(ctx, cfg, logger)
}

// RunWithLogger is Run with a caller-supplied slog.Logger.
//
// It runs the boot steps in order (see startup.steps), serves until ctx
// is cancelled, then tears down through stopDaemon. A step that fails
// stops the boot and its error is returned; cleanups registered by the
// steps that ran are released either way.
func RunWithLogger(ctx context.Context, cfg config.Config, logger *slog.Logger) error {
	s := &startup{cfg: cfg, logger: logger}
	defer s.runCleanups()
	for _, step := range s.steps() {
		if err := step(ctx); err != nil {
			return err
		}
	}

	<-ctx.Done()
	logger.Info("shutdown signal received")
	err := s.shutdown()
	logger.Info("tokenops daemon stopped")
	return err
}

// httpServer is the part of the proxy server the shutdown sequence uses.
type httpServer interface {
	Shutdown(context.Context) error
	Close() error
}

// shutdownSteps are the daemon's teardown stages, in the order
// stopDaemon runs them.
type shutdownSteps struct {
	server          httpServer
	waitSubsystems  func(time.Duration) error
	drainEvents     func(time.Duration) error
	closeComponents func()
	running         func() []string
}

// stopDaemon tears the daemon down in dependency order: stop serving,
// stop the pollers, drain the event bus, close storage. Every stage runs
// even when an earlier one fails: an HTTP request that outlives the grace
// period (a long LLM stream, routinely) must not skip the drain and lose
// the queued events. The HTTP shutdown error, if any, is returned last.
func stopDaemon(logger *slog.Logger, timeout time.Duration, steps shutdownSteps) error {
	shutdownCtx, cancel := context.WithTimeout(context.Background(), timeout+time.Second)
	defer cancel()
	var serveErr error
	// 1. Stop accepting new requests so no fresh domain events fire.
	if err := steps.server.Shutdown(shutdownCtx); err != nil && !errors.Is(err, context.Canceled) {
		logger.Warn("http shutdown incomplete; closing remaining connections", "err", err)
		_ = steps.server.Close()
		serveErr = fmt.Errorf("shutdown: %w", err)
	}
	// 2. Stop the pollers and wait for them, so no subsystem is still
	// writing when the buses below are drained. The bound is the
	// configured shutdown timeout: one poller that ignores cancellation
	// is a bug in that poller, not a reason for the daemon never to
	// exit.
	if err := steps.waitSubsystems(timeout); err != nil {
		logger.Warn("subsystem shutdown", "err", err, "running", steps.running())
	}
	// 3. Drain in-flight canonical envelopes after all publishers stop.
	if err := steps.drainEvents(timeout); err != nil {
		logger.Warn("event bus drain", "err", err)
	}
	// 4. Close storage last, once nothing writes to it.
	steps.closeComponents()
	return serveErr
}

// SignalContext returns a context cancelled on SIGINT/SIGTERM. Callers must
// invoke the returned stop function to release signal resources.
func SignalContext(parent context.Context) (context.Context, context.CancelFunc) {
	return signal.NotifyContext(parent, syscall.SIGINT, syscall.SIGTERM)
}

// resolveCertDir returns the cert directory to use, creating an absolute
// path. Empty input falls back to ~/.tokenops/certs so the daemon has a
// stable home without forcing every operator to set the path explicitly.
func resolveCertDir(configured string) (string, error) {
	if configured != "" {
		return configured, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".tokenops", "certs"), nil
}

// resolveStoragePath returns the sqlite events DB path. Defaults to
// ~/.tokenops/events.db. The parent directory is created so sqlite.Open
// has a writable home.
func resolveStoragePath(configured string) (string, error) {
	path := configured
	if path == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		path = filepath.Join(home, ".tokenops", "events.db")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", err
	}
	return path, nil
}
