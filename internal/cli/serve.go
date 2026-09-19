package cli

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"go.klarlabs.de/tokenops/internal/bootstrap"
	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/contexts/spend/session"
	"go.klarlabs.de/tokenops/internal/daemon"
	"go.klarlabs.de/tokenops/internal/events"
	"go.klarlabs.de/tokenops/internal/mcp"
	"go.klarlabs.de/tokenops/internal/storage/sqlite"
	"go.klarlabs.de/tokenops/internal/version"
	"go.klarlabs.de/tokenops/pkg/eventschema"
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
	cfg, cfgErr := loadConfig(&rootFlags{})
	if cfgErr != nil {
		logger.Warn("serve: could not load config snapshot", "err", cfgErr)
		cfg = config.Default()
	}
	// Env vars stay authoritative for serve (documented contract);
	// config file fills the gaps. loadConfig already applies
	// TOKENOPS_STORAGE_PATH / TOKENOPS_PRICING_PATH on top of the file,
	// so the fallbacks below only matter when the config failed to load.
	dbPath := cfg.Storage.Path
	if dbPath == "" {
		dbPath = os.Getenv("TOKENOPS_STORAGE_PATH")
	}
	pricingPath := cfg.Pricing.Path
	if pricingPath == "" {
		pricingPath = os.Getenv("TOKENOPS_PRICING_PATH")
	}
	components, err := bootstrap.New(ctx, bootstrap.Options{
		DBPath:      dbPath,
		Logger:      logger,
		OpenStore:   true,
		PricingPath: pricingPath,
	})
	if err != nil {
		return err
	}
	defer func() { _ = components.Close() }()

	srv := mcp.NewServer("tokenops", version.Version, logger)

	// Recorded at startup so later checks compare against the binary this
	// process really started from. serve lives as long as its MCP client,
	// across upgrades: one agent's server reported 0.54.3 while 0.66.0 was
	// installed, and nothing said so.
	var binaryDrift func() mcp.BinaryDrift
	if snap, err := mcp.SnapshotExecutable(); err == nil {
		binaryDrift = snap.Check
	} else {
		logger.Warn("serve: cannot resolve own executable; out-of-date detection off", "err", err)
	}

	// Every tool that answers from config reads it through here, at call
	// time. Agents write config.yaml mid-session (tokenops_plan_set,
	// tokenops_vendor_usage_setup), and tools holding the startup snapshot
	// kept answering with the old config until the client restarted. Nil
	// when no config loaded, which tools treat as "no config", not as an
	// empty one.
	configPath, _ := defaultConfigPath()
	var currentConfig func() *config.Config
	if cfgErr == nil {
		currentConfig = newConfigWatcher(ctx, configPath, cfg, logger).Get
	}

	// Built before the tools are registered so the analytics tools and the
	// status tool share one hook. A spend figure produced while ingestion is
	// dead is a lower bound, not a measurement, and the tools that return
	// numbers should say so rather than leaving it to whoever thinks to call
	// status first.
	var staleSources func() []config.StaleSource
	if components.Store != nil {
		staleSources = staleSourcesCheck(ctx, components.Store, currentConfig)
	}

	if err := mcp.RegisterTools(srv, mcp.Deps{
		Store:        components.Store,
		Aggregator:   components.Aggregator,
		Spend:        components.Spend,
		Waste:        cfg.Coaching.WasteConfig(),
		StaleSources: staleSources,
	}); err != nil {
		return fmt.Errorf("register tools: %w", err)
	}
	if err := mcp.RegisterRulesTools(srv); err != nil {
		return fmt.Errorf("register rules tools: %w", err)
	}
	if err := mcp.RegisterParityTools(srv, mcp.ParityDeps{
		Store:    components.Store,
		Spend:    components.Spend,
		Pipeline: buildReplayPipeline(cfg, components.Spend),
	}); err != nil {
		return fmt.Errorf("register parity tools: %w", err)
	}
	// In `serve` mode the proxy never starts, so proxy.IsReady would
	// remain false forever. Treat readiness as "store opened + tools
	// registered" — that's what serve is actually for. blockers[]
	// still surfaces disabled subsystems for the caller.
	// serve does not ingest; the daemon does. Probe for it so status can
	// say the pipeline is dead rather than answering queries against a
	// store nothing is writing. The configured listen address backs up the
	// URL hint, which has vanished under a running daemon before.
	daemonURL := mcp.ConfiguredDaemonURL(cfg)
	probe := func() mcp.DaemonReport { return mcp.ProbeDaemonAt(daemonURL) }
	deps := serveControlDeps(currentConfig, func() bool { return components.Store != nil }, staleSources, binaryDrift, probe)
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
		planDeps.ConfigGetter = currentConfig
	}
	if err := mcp.RegisterPlanTools(srv, planDeps); err != nil {
		return fmt.Errorf("register plan tools: %w", err)
	}
	if err := mcp.RegisterAgentDXTools(srv, mcp.AgentDXDeps{}); err != nil {
		return fmt.Errorf("register agent-dx tools: %w", err)
	}
	if err := mcp.RegisterStoryTools(srv, mcp.StoryDeps{}); err != nil {
		return fmt.Errorf("register story tools: %w", err)
	}
	if err := mcp.RegisterRoutingAdviceTools(srv, mcp.RoutingAdviceDeps{
		ConfigGetter: planDeps.ConfigGetter, Config: planDeps.Config, Store: planDeps.Store,
		Spend: components.Spend,
	}); err != nil {
		return fmt.Errorf("register routing advice tools: %w", err)
	}
	if err := mcp.RegisterApprovalTools(srv, mcp.ApprovalDeps{ApplyConfig: applyConfigRestart}); err != nil {
		return fmt.Errorf("register approval tools: %w", err)
	}
	if err := mcp.RegisterModeTools(srv, mcp.ModeDeps{
		ApplyConfig: applyConfigRestart, DaemonURL: daemonURL, UnitInstalled: daemon.UnitInstalled,
	}); err != nil {
		return fmt.Errorf("register mode tools: %w", err)
	}
	if err := mcp.RegisterSetupTools(srv, mcp.SetupDeps{ApplyConfig: applyConfigRestart}); err != nil {
		return fmt.Errorf("register setup tools: %w", err)
	}
	if err := mcp.RegisterHelpTool(srv); err != nil {
		return fmt.Errorf("register help tool: %w", err)
	}
	if err := mcp.RegisterDataSourcesTool(srv, mcp.DataSourcesDeps{Store: components.Store}); err != nil {
		return fmt.Errorf("register data sources tool: %w", err)
	}
	if err := mcp.RegisterDashboardTool(srv, mcp.DashboardDeps{
		DaemonURL: daemonURL, UnitInstalled: daemon.UnitInstalled,
	}); err != nil {
		return fmt.Errorf("register dashboard tool: %w", err)
	}
	if err := mcp.RegisterFmtTools(srv); err != nil {
		return fmt.Errorf("register fmt tools: %w", err)
	}
	if err := mcp.RegisterGapTools(srv, serveGapDeps(currentConfig, gapCounts(components.Store))); err != nil {
		return fmt.Errorf("register gap tools: %w", err)
	}
	if err := mcp.RegisterCoachTools(srv, mcp.CoachDeps{
		JSONLRoot: cfg.VendorUsage.ClaudeCodeJSONL.Root,
	}); err != nil {
		return fmt.Errorf("register coach tools: %w", err)
	}

	logger.Info("tokenops serve ready", "version", version.Version)
	return mcp.ServeStdio(ctx, srv, mcp.SessionMiddleware(tracker, sessionProvider))
}

// serveControlDeps wires the control tools to live state: config read at call
// time, the ingestion daemon probed per call, and this process's own binary
// checked against the install on disk.
//
// serve does not ingest; the daemon does. The probe lets status say the
// pipeline is dead rather than answering queries against a store nothing is
// writing. The daemon it finds is also the only process that can report the
// daemon's version and its domain-event counters, so both are read from it.
func serveControlDeps(current func() *config.Config, ready func() bool, stale func() []config.StaleSource, drift func() mcp.BinaryDrift, probe func() mcp.DaemonReport) mcp.ControlDeps {
	return mcp.ControlDeps{
		ConfigGetter:       current,
		ReadyCheck:         ready,
		StaleSources:       stale,
		DaemonProbe:        probe,
		DaemonVersion:      mcp.FetchDaemonVersion,
		DaemonDomainEvents: mcp.FetchDaemonDomainEvents,
		BinaryDrift:        drift,
	}
}

// serveGapDeps hands the vendor-usage tool the live config. A nil getter (no
// config loaded) disables that tool rather than reporting an empty source
// list, because "no sources configured" and "no config read" are different
// answers and only one of them is the operator's doing.
func serveGapDeps(current func() *config.Config, counts func(context.Context, time.Time, time.Time) (map[string]int64, error)) mcp.GapDeps {
	return mcp.GapDeps{ConfigGetter: current, Counts: counts}
}

// staleSourcesCheck reports enabled vendor-usage sources that have ingested
// nothing recently, so status surfaces a silently-dead poller instead of
// quietly serving $0/stale data. It reads the live config on every call: a
// source switched on through tokenops_vendor_usage_setup is exactly the one
// whose silence matters next, and a startup copy never saw it enabled.
//
// Nil without a config. Best-effort: a store error degrades to "no
// warnings", never a status failure.
func staleSourcesCheck(ctx context.Context, counter config.SourceCounter, current func() *config.Config) func() []config.StaleSource {
	if current == nil || counter == nil {
		return nil
	}
	return func() []config.StaleSource {
		cfg := current()
		if cfg == nil {
			return nil
		}
		stale, err := cfg.CheckStaleIngestion(ctx, counter, sourceProbes(*cfg), config.StaleIngestionWindow, time.Now())
		if err != nil {
			return nil
		}
		return stale
	}
}

// gapCounts adapts the store to the counting hook, or nil when storage is
// off and there is nothing to count.
func gapCounts(store *sqlite.Store) func(context.Context, time.Time, time.Time) (map[string]int64, error) {
	if store == nil {
		return nil
	}
	return store.CountBySource
}

// applyConfigRestart restarts the supervised daemon after an MCP tool writes
// config, so an agent's change is live without a command nobody ran.
func applyConfigRestart() string { return daemon.RestartForConfig().Note() }
