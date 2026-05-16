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

	"github.com/felixgeelhaar/tokenops/internal/bootstrap"
	"github.com/felixgeelhaar/tokenops/internal/config"
	"github.com/felixgeelhaar/tokenops/internal/contexts/governance/budget"
	"github.com/felixgeelhaar/tokenops/internal/contexts/observability/observ"
	"github.com/felixgeelhaar/tokenops/internal/contexts/optimization/optimizer"
	"github.com/felixgeelhaar/tokenops/internal/contexts/security/audit"
	"github.com/felixgeelhaar/tokenops/internal/contexts/security/dashauth"
	"github.com/felixgeelhaar/tokenops/internal/contexts/security/tlsmint"
	anthropicusage "github.com/felixgeelhaar/tokenops/internal/contexts/spend/vendorusage/anthropic"
	"github.com/felixgeelhaar/tokenops/internal/contexts/spend/vendorusage/claudecode"
	"github.com/felixgeelhaar/tokenops/internal/contexts/spend/vendorusage/claudecodejsonl"
	"github.com/felixgeelhaar/tokenops/internal/contexts/workflows/workflow"
	"github.com/felixgeelhaar/tokenops/internal/domainevents"
	"github.com/felixgeelhaar/tokenops/internal/events"
	"github.com/felixgeelhaar/tokenops/internal/infra/rulesfs"
	"github.com/felixgeelhaar/tokenops/internal/otlp"
	"github.com/felixgeelhaar/tokenops/internal/proxy"
	"github.com/felixgeelhaar/tokenops/internal/storage/sqlite"
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
	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("config: %w", err)
	}
	logger.Info("tokenops daemon starting",
		"version", version.Version,
		"commit", version.Commit,
		"listen", cfg.Listen,
	)

	// Composition root constructs the domain bus + counter + redactor
	// (and any other long-lived collaborators) once. Daemon never
	// allocates a fresh bus or counter — it consumes what bootstrap
	// hands back. Store opens later only when storage is enabled.
	earlyComponents, err := bootstrap.New(ctx, bootstrap.Options{
		Logger:    logger,
		OpenStore: false,
	})
	if err != nil {
		return err
	}
	dbus := earlyComponents.DomainBus
	domainEventCounter := earlyComponents.EventCounter
	workflow.SetDomainBus(dbus)
	optimizer.SetDomainBus(dbus)
	rulesfs.SetDomainBus(dbus)
	budget.SetDomainBus(dbus)
	dbus.Subscribe("*", func(ev domainevents.Event) {
		logger.Debug("domain event", "kind", ev.Kind())
	})

	// JSONL persistence so late subscribers can replay history.
	var domainLog *domainevents.JSONLog
	if cfg.Storage.Enabled {
		eventsPath, _ := resolveStoragePath(cfg.Storage.Path)
		logPath := filepath.Join(filepath.Dir(eventsPath), "domain-events.jsonl")
		if l, err := domainevents.NewJSONLog(logPath); err == nil {
			domainLog = l
			// Hydrate the in-memory counter from prior runs so dashboards
			// see continuity across restarts. Lenient mode skips bad
			// lines instead of aborting.
			if skipped, rerr := domainevents.ReplayLenient(logPath, func(r domainevents.Record) error {
				dbus.Publish(domainevents.NewReplayed(r.Kind, r.At))
				return nil
			}); rerr != nil {
				logger.Warn("domain event log replay", "err", rerr, "skipped", skipped)
			}
			domainLog.Attach(dbus, nil)
			logger.Info("domain event log ready", "path", logPath)
		} else {
			logger.Warn("domain event log unavailable", "err", err)
		}
	}

	// Async dispatch with bounded queue isolates the publisher hot
	// path from any slow subscriber. Started AFTER subscribers wire so
	// the worker sees them on first dispatch.
	dbus.StartAsync(1024)

	routes, err := proxy.BuildProviderRoutes(cfg.Providers)
	if err != nil {
		return fmt.Errorf("provider routes: %w", err)
	}

	opts := []proxy.Option{
		proxy.WithLogger(logger),
		proxy.WithShutdownTimeout(cfg.Shutdown.Timeout),
		proxy.WithProviderRoutes(routes),
		proxy.WithEventCounts(domainEventCounter.Counts),
	}
	if cfg.Resilience.Enabled {
		opts = append(opts, proxy.WithResilience(proxy.ResilienceConfig{
			FirstByteTimeout: cfg.Resilience.FirstByteTimeout,
			IdleTimeout:      cfg.Resilience.IdleTimeout,
			TotalTimeout:     cfg.Resilience.TotalTimeout,
			FailureThreshold: cfg.Resilience.FailureThreshold,
		}))
		logger.Info("resilience enabled",
			"first_byte_timeout", cfg.Resilience.FirstByteTimeout,
			"idle_timeout", cfg.Resilience.IdleTimeout,
			"total_timeout", cfg.Resilience.TotalTimeout,
			"failure_threshold", cfg.Resilience.FailureThreshold,
		)
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
		store   *sqlite.Store
		bus     *events.AsyncBus
		dashTok string
	)
	components := earlyComponents
	if cfg.Storage.Enabled {
		path, err := resolveStoragePath(cfg.Storage.Path)
		if err != nil {
			return fmt.Errorf("storage path: %w", err)
		}
		if err := components.OpenStoreAt(ctx, path); err != nil {
			return err
		}
		store = components.Store

		// Audit recorder subscribes to security-relevant domain events.
		// Wired here (after the store opens) rather than at the dbus
		// init block above because audit requires persistence.
		auditSub := audit.Subscribe(dbus, audit.NewRecorder(store), logger, "daemon")
		if auditSub != nil {
			opts = append(opts, proxy.WithAuditDrops(auditSub.DroppedCount))
			defer auditSub.Close()
		}

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
				expOpts.Redactor = earlyComponents.Redactor
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
			proxy.WithTokenizer(components.Tokenizers),
		)

		// Optional vendor-usage pollers. Each one publishes envelopes
		// through the same bus the proxy uses, so vendor and proxy
		// signals end up in the same store with distinct Source tags
		// (signal_quality reads those tags to upgrade confidence).
		if cfg.VendorUsage.ClaudeCode.Enabled {
			p := claudecode.NewPoller(bus, claudecode.PollerOptions{
				Path:     cfg.VendorUsage.ClaudeCode.Path,
				Interval: cfg.VendorUsage.ClaudeCode.Interval,
				Logger:   logger,
			})
			go func() {
				if err := p.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
					logger.Warn("claude-code stats poller exited", "err", err)
				}
			}()
			logger.Warn("claude-code stats cache poller is DEPRECATED — switch to vendor_usage.claude_code_jsonl for live per-turn data",
				"interval", cfg.VendorUsage.ClaudeCode.Interval,
				"path", cfg.VendorUsage.ClaudeCode.Path,
			)
		}
		if cfg.VendorUsage.ClaudeCodeJSONL.Enabled {
			p := claudecodejsonl.NewPoller(bus, claudecodejsonl.PollerOptions{
				Root:     cfg.VendorUsage.ClaudeCodeJSONL.Root,
				Interval: cfg.VendorUsage.ClaudeCodeJSONL.Interval,
				Logger:   logger,
			})
			go func() {
				if err := p.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
					logger.Warn("claude-code jsonl poller exited", "err", err)
				}
			}()
			logger.Info("claude-code jsonl poller live",
				"interval", cfg.VendorUsage.ClaudeCodeJSONL.Interval,
				"root", cfg.VendorUsage.ClaudeCodeJSONL.Root,
			)
		}
		if cfg.VendorUsage.Anthropic.Enabled {
			client := anthropicusage.NewAdminClient(cfg.VendorUsage.Anthropic.AdminKey)
			p := anthropicusage.NewPoller(client, bus, anthropicusage.PollerOptions{
				AdminKey:    cfg.VendorUsage.Anthropic.AdminKey,
				Interval:    cfg.VendorUsage.Anthropic.Interval,
				BucketWidth: anthropicusage.BucketWidth(cfg.VendorUsage.Anthropic.BucketWidth),
				Logger:      logger,
			})
			go func() {
				if err := p.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
					logger.Warn("anthropic admin usage poller exited", "err", err)
				}
			}()
			logger.Info("anthropic admin usage poller live",
				"interval", cfg.VendorUsage.Anthropic.Interval,
				"bucket_width", cfg.VendorUsage.Anthropic.BucketWidth,
			)
		}

		analyticsH, err := proxy.NewAnalyticsHandlers(components.Store, components.Aggregator, components.Spend)
		if err != nil {
			return fmt.Errorf("analytics handlers: %w", err)
		}
		opts = append(opts, proxy.WithAnalytics(analyticsH))
		opts = append(opts, proxy.WithAudit(proxy.NewAuditHandlers(components.Store)))

		// Dashboard + /api/* are protected by a shared-secret token.
		// Either the operator sets cfg.Dashboard.AdminToken via env /
		// config, or the daemon mints and persists one on first start.
		// MCP tokenops_dashboard surfaces the token so the agent hands
		// the operator a clickable URL that auto-authenticates.
		tok, errTok := loadOrMintDashToken(cfg.Dashboard.AdminToken)
		if errTok != nil {
			return fmt.Errorf("dashboard token: %w", errTok)
		}
		dashTok = tok
		auth, err := dashauth.New(dashauth.Config{
			AdminToken:   dashTok,
			SessionTTL:   24 * time.Hour,
			CookieSecure: cfg.TLS.Enabled,
		})
		if err != nil {
			return fmt.Errorf("dashboard auth: %w", err)
		}
		opts = append(opts, proxy.WithDashAuth(auth))
	}

	if cfg.Rules.Enabled {
		root := cfg.Rules.Root
		if root == "" {
			if wd, err := os.Getwd(); err == nil {
				root = wd
			} else {
				root = "."
			}
		}
		rulesH, err := proxy.NewRulesHandlers(root, cfg.Rules.RepoID)
		if err != nil {
			return fmt.Errorf("rules handlers: %w", err)
		}
		rulesH.AttachDomainBus(dbus)
		opts = append(opts, proxy.WithRules(rulesH))
		logger.Info("rule intelligence enabled", "root", root, "repo_id", cfg.Rules.RepoID)
	}

	srv := proxy.New(cfg.Listen, opts...)
	if err := srv.Start(ctx); err != nil {
		return fmt.Errorf("start proxy: %w", err)
	}
	// Try to advertise the daemon as tokenops.local over mDNS so the
	// dashboard URL stays memorable. Best-effort: container hosts,
	// firewalled networks, and CI runners frequently lack a usable
	// multicast interface — we log + fall back to the loopback URL
	// instead of failing the boot.
	var (
		mdnsClose      = func() {}
		mdnsPublicURL  string
		mdnsAdvertised bool
	)
	if closer, publicURL, err := startMDNSAdvertise(srv.Addr(), srv.TLSEnabled()); err != nil {
		logger.Info("mdns advertise unavailable; using loopback URL", "err", err)
	} else {
		mdnsClose = closer
		mdnsPublicURL = publicURL
		mdnsAdvertised = true
		logger.Info("mdns advertise live", "url", publicURL)
	}
	defer mdnsClose()
	// Publish the listen URL so the MCP `serve` process can return a
	// clickable dashboard link via tokenops_dashboard. Removed on
	// shutdown so a stale URL never survives the daemon. Failure here
	// is non-fatal: the daemon stays up; the MCP tool just falls
	// back to "run tokenops up" guidance.
	if hintPath, err := writeURLHint(srv.Addr(), srv.TLSEnabled(), mdnsPublicURL, dashTok); err != nil {
		logger.Warn("could not publish daemon URL hint", "err", err)
	} else {
		logger.Info("daemon URL hint published",
			"path", hintPath,
			"mdns_advertised", mdnsAdvertised,
		)
		defer func() {
			if err := removeURLHint(); err != nil {
				logger.Warn("could not remove daemon URL hint", "err", err)
			}
		}()
	}
	// Publish blockers + remediation hints so /readyz exposes the same
	// signal the MCP tokenops_status tool surfaces. Operators on a fresh
	// install (storage/rules/providers off) see exactly what to fix
	// without grepping config.
	blockers := cfg.Blockers()
	proxy.SetReadyState(blockers, config.NextActionsFor(blockers))
	if len(blockers) > 0 {
		logger.Info("daemon started with blockers", "blockers", blockers)
	}
	proxy.MarkReady(true)

	<-ctx.Done()
	logger.Info("shutdown signal received")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.Shutdown.Timeout+time.Second)
	defer cancel()
	// 1. Stop accepting new requests so no fresh domain events fire.
	if err := srv.Shutdown(shutdownCtx); err != nil && !errors.Is(err, context.Canceled) {
		return fmt.Errorf("shutdown: %w", err)
	}
	// 2. Drain in-flight telemetry envelopes.
	if bus != nil {
		if err := bus.Close(cfg.Shutdown.Timeout); err != nil {
			logger.Warn("event bus drain", "err", err)
		}
		logger.Info("event bus drained",
			"published", bus.PublishedCount(),
			"dropped", bus.DroppedCount(),
		)
	}
	// 3. Drain the domain bus with the same timeout — slow subscribers
	// don't block daemon exit beyond cfg.Shutdown.Timeout.
	if !dbus.CloseWithTimeout(cfg.Shutdown.Timeout) {
		logger.Warn("domain bus drain timed out", "timeout", cfg.Shutdown.Timeout)
	}
	// 4. Persistence after bus drain so the last JSONL entry lands.
	if domainLog != nil {
		_ = domainLog.Close()
	}
	if components != nil {
		_ = components.Shutdown()
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
