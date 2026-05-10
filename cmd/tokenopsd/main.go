// Command tokenopsd is the TokenOps daemon. It is intentionally a thin
// wrapper around internal/daemon: lifecycle, proxy, and shutdown all live
// there so the tokenops CLI start subcommand and tokenopsd share one boot
// sequence.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/felixgeelhaar/tokenops/internal/config"
	"github.com/felixgeelhaar/tokenops/internal/daemon"
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

	ctx, stop := daemon.SignalContext(context.Background())
	defer stop()
	return daemon.Run(ctx, cfg, os.Stderr)
}

func printUsage(w *os.File) {
	_, _ = fmt.Fprintln(w, `tokenopsd — TokenOps daemon

usage:
  tokenopsd [start] [--config path]
  tokenopsd version
  tokenopsd help`)
}
