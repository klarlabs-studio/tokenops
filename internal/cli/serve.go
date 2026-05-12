package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/felixgeelhaar/tokenops/internal/bootstrap"
	"github.com/felixgeelhaar/tokenops/internal/mcp"
	"github.com/felixgeelhaar/tokenops/internal/proxy"
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
	logger := slog.New(slog.NewTextHandler(cmd.ErrOrStderr(), &slog.HandlerOptions{Level: slog.LevelInfo}))
	components, err := bootstrap.New(ctx, bootstrap.Options{
		DBPath:    os.Getenv("TOKENOPS_STORAGE_PATH"),
		Logger:    logger,
		OpenStore: true,
	})
	if err != nil {
		return err
	}
	defer func() { _ = components.Close() }()

	srv := mcp.NewServer("tokenops", version.Version, logger)
	if err := mcp.RegisterTools(srv, mcp.Deps{
		Store:      components.Store,
		Aggregator: components.Aggregator,
		Spend:      components.Spend,
	}); err != nil {
		return fmt.Errorf("register tools: %w", err)
	}
	if err := mcp.RegisterRulesTools(srv); err != nil {
		return fmt.Errorf("register rules tools: %w", err)
	}
	if err := mcp.RegisterParityTools(srv, mcp.ParityDeps{Store: components.Store, Spend: components.Spend}); err != nil {
		return fmt.Errorf("register parity tools: %w", err)
	}
	cfg, cfgErr := loadConfig(&rootFlags{})
	if cfgErr != nil {
		logger.Warn("serve: could not load config snapshot", "err", cfgErr)
	}
	var configJSON json.RawMessage
	if cfgErr == nil {
		if data, sErr := cfg.Snapshot(); sErr == nil {
			configJSON = data
		}
	}
	if err := mcp.RegisterControlTools(srv, mcp.ControlDeps{
		ConfigJSON: configJSON,
		ReadyCheck: proxy.IsReady,
	}); err != nil {
		return fmt.Errorf("register control tools: %w", err)
	}

	logger.Info("tokenops serve ready", "version", version.Version)
	return mcp.ServeStdio(ctx, srv)
}
