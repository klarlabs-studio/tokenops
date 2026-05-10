// Command tokenops-mcp is the TokenOps Model Context Protocol server. It
// reads JSON-RPC 2.0 from stdin and writes responses to stdout, exposing
// spend / forecast / workflow queries against the local event store as
// MCP tools.
//
// Wire it into Claude Desktop, Cursor, or any MCP client by registering:
//
//	{
//	  "mcpServers": {
//	    "tokenops": {
//	      "command": "tokenops-mcp",
//	      "env": {"TOKENOPS_STORAGE_PATH": "/Users/me/.tokenops/events.db"}
//	    }
//	  }
//	}
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/felixgeelhaar/tokenops/internal/analytics"
	"github.com/felixgeelhaar/tokenops/internal/mcp"
	"github.com/felixgeelhaar/tokenops/internal/spend"
	"github.com/felixgeelhaar/tokenops/internal/storage/sqlite"
	"github.com/felixgeelhaar/tokenops/internal/version"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "tokenops-mcp: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	dbPath := os.Getenv("TOKENOPS_STORAGE_PATH")
	if dbPath == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("resolve home: %w", err)
		}
		dbPath = filepath.Join(home, ".tokenops", "events.db")
	}

	store, err := sqlite.Open(ctx, dbPath, sqlite.Options{})
	if err != nil {
		return fmt.Errorf("open events.db at %s: %w", dbPath, err)
	}
	defer func() { _ = store.Close() }()

	spendEng := spend.NewEngine(spend.DefaultTable())
	agg := analytics.New(store, spendEng)

	// Logger writes to stderr — stdout is reserved for JSON-RPC responses.
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	srv := mcp.NewServer("tokenops", version.Version, logger)
	if err := mcp.RegisterTools(srv, mcp.Deps{
		Store:      store,
		Aggregator: agg,
		Spend:      spendEng,
	}); err != nil {
		return fmt.Errorf("register tools: %w", err)
	}

	logger.Info("tokenops-mcp ready",
		"db", dbPath,
		"version", version.Version)
	return srv.Serve(ctx, os.Stdin, os.Stdout)
}
