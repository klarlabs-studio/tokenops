// Command tokenopsd is the TokenOps daemon.
//
// The daemon hosts the local proxy and supporting subsystems. The skeleton
// implemented here boots the HTTP listener, structured logger, health
// endpoints, and graceful shutdown. Provider routing, optimization,
// observability, and storage layers are wired in by their dedicated tasks.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/felixgeelhaar/tokenops/internal/config"
	"github.com/felixgeelhaar/tokenops/internal/observ"
	"github.com/felixgeelhaar/tokenops/internal/proxy"
	"github.com/felixgeelhaar/tokenops/internal/version"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return runStart(args)
	}
	switch args[0] {
	case "start":
		return runStart(args[1:])
	case "version", "--version", "-v":
		fmt.Println("tokenopsd", version.String())
		return nil
	case "help", "--help", "-h":
		printUsage(os.Stdout)
		return nil
	default:
		printUsage(os.Stderr)
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func runStart(args []string) error {
	fs := flag.NewFlagSet("start", flag.ContinueOnError)
	configPath := fs.String("config", "", "path to config.yaml (optional)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}

	logger := observ.NewLogger(os.Stderr, cfg.Log.Level, cfg.Log.Format)
	logger.Info("tokenopsd starting",
		"version", version.Version,
		"commit", version.Commit,
		"listen", cfg.Listen,
	)

	ctx, stop := signal.NotifyContext(context.Background(),
		syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	routes, err := proxy.BuildProviderRoutes(cfg.Providers)
	if err != nil {
		return fmt.Errorf("provider routes: %w", err)
	}

	srv := proxy.New(cfg.Listen,
		proxy.WithLogger(logger),
		proxy.WithShutdownTimeout(cfg.Shutdown.Timeout),
		proxy.WithProviderRoutes(routes),
	)
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
	logger.Info("tokenopsd stopped")
	return nil
}

func printUsage(w *os.File) {
	_, _ = fmt.Fprintln(w, `tokenopsd — TokenOps daemon

usage:
  tokenopsd [start] [--config path]
  tokenopsd version
  tokenopsd help`)
}
