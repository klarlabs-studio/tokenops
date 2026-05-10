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

	"github.com/felixgeelhaar/tokenops/internal/analytics"
	"github.com/felixgeelhaar/tokenops/internal/config"
	"github.com/felixgeelhaar/tokenops/internal/events"
	"github.com/felixgeelhaar/tokenops/internal/observ"
	"github.com/felixgeelhaar/tokenops/internal/otlp"
	"github.com/felixgeelhaar/tokenops/internal/proxy"
	"github.com/felixgeelhaar/tokenops/internal/redaction"
	"github.com/felixgeelhaar/tokenops/internal/spend"
	"github.com/felixgeelhaar/tokenops/internal/storage/sqlite"
	"github.com/felixgeelhaar/tokenops/internal/tlsmint"
	"github.com/felixgeelhaar/tokenops/internal/tokenizer"
	"github.com/felixgeelhaar/tokenops/internal/version"
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
func RunWithLogger(ctx context.Context, cfg config.Config, logger *slog.Logger) error {
	logger.Info("tokenops daemon starting",
		"version", version.Version,
		"commit", version.Commit,
		"listen", cfg.Listen,
	)

	routes, err := proxy.BuildProviderRoutes(cfg.Providers)
	if err != nil {
		return fmt.Errorf("provider routes: %w", err)
	}

	opts := []proxy.Option{
		proxy.WithLogger(logger),
		proxy.WithShutdownTimeout(cfg.Shutdown.Timeout),
		proxy.WithProviderRoutes(routes),
	}
	if cfg.TLS.Enabled {
		certDir, err := resolveCertDir(cfg.TLS.CertDir)
		if err != nil {
			return fmt.Errorf("tls cert dir: %w", err)
		}
		bundle, err := tlsmint.EnsureBundle(certDir, tlsmint.Options{
			Hostnames: cfg.TLS.Hostnames,
		})
		if err != nil {
			return fmt.Errorf("tls bundle: %w", err)
		}
		logger.Info("tls bundle ready",
			"cert_dir", bundle.Dir,
			"leaf_not_after", bundle.LeafCert.NotAfter,
		)
		opts = append(opts, proxy.WithTLS(bundle.TLSConfig()))
	}

	var (
		store *sqlite.Store
		bus   *events.AsyncBus
	)
	if cfg.Storage.Enabled {
		path, err := resolveStoragePath(cfg.Storage.Path)
		if err != nil {
			return fmt.Errorf("storage path: %w", err)
		}
		s, err := sqlite.Open(ctx, path, sqlite.Options{})
		if err != nil {
			return fmt.Errorf("open storage: %w", err)
		}
		store = s

		var sinks []events.Sink
		sinks = append(sinks, store)

		if cfg.OTel.Enabled {
			expOpts := otlp.Options{
				Endpoint:       cfg.OTel.Endpoint,
				Headers:        cfg.OTel.Headers,
				ServiceName:    cfg.OTel.ServiceName,
				ServiceVersion: cfg.OTel.ServiceVersion,
				Logger:         logger,
			}
			if cfg.OTel.RedactEnabled() {
				expOpts.Redactor = redaction.New(redaction.Config{})
			}
			exporter, err := otlp.New(expOpts)
			if err != nil {
				return fmt.Errorf("otlp exporter: %w", err)
			}
			sinks = append(sinks, exporter)
			logger.Info("otlp exporter ready",
				"endpoint", cfg.OTel.Endpoint,
				"redact", cfg.OTel.RedactEnabled(),
			)
		}

		bus = events.NewAsync(events.NewMultiSink(sinks...), events.Options{Logger: logger})
		logger.Info("event store ready", "path", path)
		opts = append(opts,
			proxy.WithEventBus(bus),
			proxy.WithTokenizer(tokenizer.NewRegistry()),
		)

		spendEng := spend.NewEngine(spend.DefaultTable())
		agg := analytics.New(store, spendEng)
		analyticsH, err := proxy.NewAnalyticsHandlers(store, agg, spendEng)
		if err != nil {
			return fmt.Errorf("analytics handlers: %w", err)
		}
		opts = append(opts, proxy.WithAnalytics(analyticsH))
	}

	srv := proxy.New(cfg.Listen, opts...)
	if err := srv.Start(ctx); err != nil {
		return fmt.Errorf("start proxy: %w", err)
	}
	proxy.MarkReady(true)

	<-ctx.Done()
	logger.Info("shutdown signal received")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.Shutdown.Timeout+time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil && !errors.Is(err, context.Canceled) {
		return fmt.Errorf("shutdown: %w", err)
	}
	if bus != nil {
		if err := bus.Close(cfg.Shutdown.Timeout); err != nil {
			logger.Warn("event bus drain", "err", err)
		}
		logger.Info("event bus drained",
			"published", bus.PublishedCount(),
			"dropped", bus.DroppedCount(),
		)
	}
	if store != nil {
		_ = store.Close()
	}
	logger.Info("tokenops daemon stopped")
	return nil
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
