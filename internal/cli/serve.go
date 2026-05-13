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
	"github.com/felixgeelhaar/tokenops/internal/contexts/spend/session"
	"github.com/felixgeelhaar/tokenops/internal/events"
	"github.com/felixgeelhaar/tokenops/internal/mcp"
	"github.com/felixgeelhaar/tokenops/internal/version"
	"github.com/felixgeelhaar/tokenops/pkg/eventschema"
)

// inferSessionProvider returns the single configured provider for
// stamping session-ping events. When zero or multiple providers are
// configured, returns ProviderUnknown so consumption math can still
// roll up but operators see a clear unattributed bucket.
func inferSessionProvider(plans map[string]string) eventschema.Provider {
	if len(plans) != 1 {
		return eventschema.ProviderUnknown
	}
	for provider := range plans {
		return eventschema.Provider(provider)
	}
	return eventschema.ProviderUnknown
}

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
	configPath, _ := defaultConfigPath()
	var watcher *configWatcher
	if cfgErr == nil {
		watcher = newConfigWatcher(ctx, configPath, cfg, logger)
	}
	var configJSON json.RawMessage
	if cfgErr == nil {
		if data, sErr := cfg.Snapshot(); sErr == nil {
			configJSON = data
		}
	}
	// In `serve` mode the proxy never starts, so proxy.IsReady would
	// remain false forever. Treat readiness as "store opened + tools
	// registered" — that's what serve is actually for. blockers[]
	// still surfaces disabled subsystems for the caller.
	deps := mcp.ControlDeps{
		ConfigJSON: configJSON,
		ReadyCheck: func() bool { return components.Store != nil },
	}
	if cfgErr == nil {
		deps.Config = &cfg
	}
	if err := mcp.RegisterControlTools(srv, deps); err != nil {
		return fmt.Errorf("register control tools: %w", err)
	}
	// Session observer: each call to tokenops_plan_headroom (or
	// related tools) lands as a plan_included PromptEvent so headroom
	// math reflects MCP-resident activity even when no traffic flows
	// through the proxy. Provider is inferred from Plans when a
	// single binding is configured; ambiguous deployments tag the
	// envelope as ProviderUnknown.
	var sessionBus events.Bus
	if components.Store != nil {
		ab := events.NewAsync(events.NewMultiSink(components.Store), events.Options{Logger: logger})
		sessionBus = ab
		defer func() { _ = ab.Close(0) }()
	}
	sessionProvider := inferSessionProvider(cfg.Plans)
	tracker := session.New(sessionBus, session.Options{Provider: sessionProvider})

	planDeps := mcp.PlanDeps{Store: components.Store, Tracker: tracker, Provider: sessionProvider}
	if cfgErr == nil {
		planDeps.Config = &cfg
		if watcher != nil {
			planDeps.ConfigGetter = watcher.Get
		}
	}
	if err := mcp.RegisterPlanTools(srv, planDeps); err != nil {
		return fmt.Errorf("register plan tools: %w", err)
	}
	if err := mcp.RegisterHelpTool(srv); err != nil {
		return fmt.Errorf("register help tool: %w", err)
	}
	if err := mcp.RegisterDataSourcesTool(srv, mcp.DataSourcesDeps{Store: components.Store}); err != nil {
		return fmt.Errorf("register data sources tool: %w", err)
	}

	logger.Info("tokenops serve ready", "version", version.Version)
	return mcp.ServeStdio(ctx, srv, mcp.SessionMiddleware(tracker, sessionProvider))
}
