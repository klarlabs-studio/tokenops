package cli

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/felixgeelhaar/tokenops/internal/analytics"
	"github.com/felixgeelhaar/tokenops/internal/mcp"
	"github.com/felixgeelhaar/tokenops/internal/spend"
	"github.com/felixgeelhaar/tokenops/internal/storage/sqlite"
	"github.com/felixgeelhaar/tokenops/internal/version"
)

func newServeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "serve",
		Short: "Start the TokenOps MCP server over stdio",
		Long: `serve starts the TokenOps MCP (Model Context Protocol) server,
reading JSON-RPC 2.0 requests from stdin and writing responses to stdout.

The server exposes spend, forecast, and workflow trace queries as MCP
tools, backed by the local SQLite event store.

Environment variables:
  TOKENOPS_STORAGE_PATH   Path to events.db (default ~/.tokenops/events.db)

Wire into any MCP client (Claude Desktop, Cursor, opencode, etc.):

  {
    "mcpServers": {
      "tokenops": {
        "command": "tokenops",
        "args": ["serve"]
      }
    }
  }`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, cancel := signal.NotifyContext(cmd.Context(), syscall.SIGINT, syscall.SIGTERM)
			defer cancel()
			return serveMCP(ctx, cmd)
		},
	}
}

func serveMCP(ctx context.Context, cmd *cobra.Command) error {
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

	logger := slog.New(slog.NewTextHandler(cmd.ErrOrStderr(), &slog.HandlerOptions{Level: slog.LevelInfo}))

	srv := mcp.NewServer("tokenops", version.Version, logger)
	if err := mcp.RegisterTools(srv, mcp.Deps{
		Store:      store,
		Aggregator: agg,
		Spend:      spendEng,
	}); err != nil {
		return fmt.Errorf("register tools: %w", err)
	}

	logger.Info("tokenops serve ready",
		"db", dbPath,
		"version", version.Version)
	return srv.Serve(ctx, cmd.InOrStdin(), cmd.OutOrStdout())
}
