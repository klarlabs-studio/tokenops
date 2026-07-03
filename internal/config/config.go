// Package config loads TokenOps daemon configuration from a YAML file with
// environment-variable overrides. The schema is intentionally small at this
// stage; subsequent tasks (proxy-providers, optimizer, observability) extend
// it with their own sections.
package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"go.klarlabs.de/tokenops/internal/contexts/coaching/waste"
	"go.klarlabs.de/tokenops/internal/contexts/governance/budget"
	"go.klarlabs.de/tokenops/internal/contexts/optimization/optimizer/router"
	"go.klarlabs.de/tokenops/internal/contexts/spend/plans"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// Mode values. Passive collects + analyzes on demand (default); Active
// additionally intervenes: the proxy applies routing rules to live
// traffic and the daemon runs the background spend watcher.
const (
	ModePassive = "passive"
	ModeActive  = "active"
)

// Config is the root daemon configuration.
type Config struct {
	// Mode selects how TokenOps helps: "passive" (analytics only —
	// observe, store, answer queries) or "active" (passive + live
	// interventions: routing rules applied to proxied traffic, budget /
	// unpriced-model watcher emitting alerts). Default passive.
	Mode      string            `yaml:"mode"`
	Listen    string            `yaml:"listen"`
	Log       LogConfig         `yaml:"log"`
	Shutdown  ShutdownConfig    `yaml:"shutdown"`
	Providers map[string]string `yaml:"providers"`
	// Plans maps provider name → plan catalog identifier (e.g.
	// "anthropic" → "claude-max-20x"). Requests routed to a provider with
	// a configured plan are billed as plan_included (CostUSD=0) and
	// roll up to the plan's monthly quota instead. See
	// internal/contexts/spend/plans for the catalog.
	Plans       map[string]string `yaml:"plans"`
	TLS         TLSConfig         `yaml:"tls"`
	Storage     StorageConfig     `yaml:"storage"`
	OTel        OTelConfig        `yaml:"otel"`
	Rules       RulesConfig       `yaml:"rules"`
	Resilience  ResilienceConfig  `yaml:"resilience"`
	VendorUsage VendorUsageConfig `yaml:"vendor_usage"`
	Dashboard   DashboardConfig   `yaml:"dashboard"`
	Pricing     PricingConfig     `yaml:"pricing"`
	Optimizer   OptimizerConfig   `yaml:"optimizer"`
	Coaching    CoachingConfig    `yaml:"coaching"`
	Budgets     []BudgetConfig    `yaml:"budgets"`
	Watch       WatchConfig       `yaml:"watch"`
}

// ActiveMode reports whether interventions (live routing, spend
// watcher) are enabled. Empty Mode means passive.
func (c Config) ActiveMode() bool { return strings.EqualFold(c.Mode, ModeActive) }

// BudgetConfig is one spend limit the watcher (active mode) and
// on-demand evaluations check. Window is a calendar window in UTC.
type BudgetConfig struct {
	Name     string  `yaml:"name"`
	Window   string  `yaml:"window"` // daily | weekly | monthly
	LimitUSD float64 `yaml:"limit_usd"`
	// WarnAt / CritAt are fractional thresholds of LimitUSD; zero falls
	// back to the budget engine defaults (0.75 / 0.95).
	WarnAt float64 `yaml:"warn_at"`
	CritAt float64 `yaml:"crit_at"`
	// WorkflowID / AgentID optionally scope the limit to one workflow
	// or agent; empty applies to all spend.
	WorkflowID string `yaml:"workflow_id"`
	AgentID    string `yaml:"agent_id"`
	// Basis selects what the limit watches: "spend" (default — real
	// billed cost) or "equivalent" (API list-price value, including
	// plan-covered usage). Flat-plan deployments use "equivalent" since
	// their real spend is always ~0.
	Basis string `yaml:"basis"`
}

// BudgetLimits maps the configured budgets into the budget engine's
// domain type.
func (c Config) BudgetLimits() []budget.Limit {
	if len(c.Budgets) == 0 {
		return nil
	}
	out := make([]budget.Limit, 0, len(c.Budgets))
	for _, b := range c.Budgets {
		out = append(out, budget.Limit{
			Name:       b.Name,
			Window:     budget.Window(strings.ToLower(b.Window)),
			LimitUSD:   b.LimitUSD,
			WarnAt:     b.WarnAt,
			CritAt:     b.CritAt,
			WorkflowID: b.WorkflowID,
			AgentID:    b.AgentID,
			Basis:      strings.ToLower(b.Basis),
		})
	}
	return out
}

// WatchConfig tunes the active-mode background watcher.
type WatchConfig struct {
	// Interval between watcher evaluations. Default 15m, minimum 1m.
	Interval time.Duration `yaml:"interval"`
}

// EffectiveInterval returns the watcher cadence with defaults applied.
func (w WatchConfig) EffectiveInterval() time.Duration {
	if w.Interval <= 0 {
		return 15 * time.Minute
	}
	if w.Interval < time.Minute {
		return time.Minute
	}
	return w.Interval
}

// CoachingConfig tunes the waste detector behind `tokenops replay
// --workflow`, the tokenops_workflow_trace MCP tool, and the dashboard
// workflow view.
type CoachingConfig struct {
	// ContextLimits override the waste detector's context thresholds per
	// workflow-ID prefix. A matching entry replaces the built-in
	// profiles ("claude-code:", "codex:"); zero fields inherit the
	// detector defaults.
	ContextLimits []ContextLimitConfig `yaml:"context_limits"`
}

// ContextLimitConfig is one per-prefix threshold override.
type ContextLimitConfig struct {
	WorkflowPrefix           string `yaml:"workflow_prefix"`
	MaxContextTokens         int64  `yaml:"max_context_tokens"`
	ContextGrowthLimitTokens int64  `yaml:"context_growth_limit_tokens"`
	MaxConsecutiveAgentLoops int    `yaml:"max_consecutive_agent_loops"`
	SystemRedundancyMin      int    `yaml:"system_redundancy_min"`
}

// WasteConfig maps coaching.context_limits into the waste detector's
// domain config. Shared by every adapter (CLI replay, MCP, dashboard)
// so all surfaces apply identical thresholds.
func (c CoachingConfig) WasteConfig() waste.Config {
	if len(c.ContextLimits) == 0 {
		return waste.Config{}
	}
	profiles := make([]waste.Profile, 0, len(c.ContextLimits))
	for _, l := range c.ContextLimits {
		profiles = append(profiles, waste.Profile{
			WorkflowPrefix:           l.WorkflowPrefix,
			MaxContextTokens:         l.MaxContextTokens,
			ContextGrowthLimitTokens: l.ContextGrowthLimitTokens,
			MaxConsecutiveAgentLoops: l.MaxConsecutiveAgentLoops,
			SystemRedundancyMin:      l.SystemRedundancyMin,
		})
	}
	return waste.Config{Profiles: profiles}
}

// OptimizerConfig tunes the optimizer pipeline shared by `tokenops
// replay` and the `tokenops_replay` MCP tool.
type OptimizerConfig struct {
	// RoutingRules feed the model-routing optimizer: requests for
	// from_model are evaluated as if routed to to_model, and the replay
	// surfaces the projected $ savings. Empty leaves the router out of
	// the pipeline.
	RoutingRules []RoutingRuleConfig `yaml:"routing_rules"`
	// RoutingMinQuality is the quality floor below which a routing rule
	// is skipped silently. Zero falls back to the router default (0.7).
	RoutingMinQuality float64 `yaml:"routing_min_quality"`
	// CommandFmt configures deterministic command-output compression
	// (the `tokenops fmt` wrapper and the proxy tool-output optimizer).
	CommandFmt CommandFmtConfig `yaml:"command_fmt"`
}

// CommandFmtConfig tunes deterministic command-output compression. Loss is
// configured per command: Default applies to any command without an entry,
// Overrides maps a command token (e.g. "git", "docker") to its own level.
// Valid levels: "conservative", "balanced", "aggressive".
type CommandFmtConfig struct {
	// Default is the loss level for commands without an override. Empty
	// means "conservative" (noise-only, nothing semantic dropped).
	Default string `yaml:"default"`
	// Overrides maps a command token to its loss level.
	Overrides map[string]string `yaml:"overrides"`
	// EmitEvents, when true, makes `tokenops fmt` append an
	// OptimizationEvent (kind=command_fmt) to the local events store on
	// each compressed run so the dashboard and scorecard count the
	// savings. Best-effort: a store error never fails the wrapped command.
	EmitEvents bool `yaml:"emit_events"`
	// Formatters holds user-defined command formatters. They extend or
	// override the built-in catalog without recompiling: name a command,
	// list the regexes that mark critical lines (always preserved), and the
	// noise regexes to drop per loss level. User rules run through the same
	// critical-line survival guard as built-ins.
	Formatters []CommandFmtFormatter `yaml:"formatters"`
}

// CommandFmtFormatter is one user-defined formatter declaration.
type CommandFmtFormatter struct {
	// Command is the token this formatter handles (e.g. "mytool"). A
	// command matching a built-in overrides it.
	Command string `yaml:"command"`
	// Aliases are extra command tokens routed to this formatter.
	Aliases []string `yaml:"aliases"`
	// Critical lists regexes; a line matching any is preserved at every
	// loss level.
	Critical []string `yaml:"critical"`
	// Drop lists noise regexes removed at balanced and/or aggressive.
	Drop CommandFmtDrop `yaml:"drop"`
}

// CommandFmtDrop lists per-level noise regexes. Balanced rules apply at
// balanced AND aggressive; Aggressive rules apply only at aggressive.
type CommandFmtDrop struct {
	Balanced   []string `yaml:"balanced"`
	Aggressive []string `yaml:"aggressive"`
}

// RoutingRuleConfig is one "route X to Y" entry. FromModel supports a
// trailing "*" prefix match (e.g. "claude-fable-5*").
type RoutingRuleConfig struct {
	Provider  string `yaml:"provider"`
	FromModel string `yaml:"from_model"`
	ToModel   string `yaml:"to_model"`
	// Quality is the operator's confidence (0.0–1.0] that ToModel
	// preserves task quality for this traffic.
	Quality   float64  `yaml:"quality"`
	Fallbacks []string `yaml:"fallbacks"`
}

// RouterConfig maps optimizer.routing_rules into the router's domain
// config. Returns nil when no rules are configured. Shared by the CLI
// replay, MCP serve, and the daemon's active-mode proxy so all
// surfaces route identically.
func (o OptimizerConfig) RouterConfig() *router.Config {
	if len(o.RoutingRules) == 0 {
		return nil
	}
	rules := make([]router.Rule, 0, len(o.RoutingRules))
	for _, r := range o.RoutingRules {
		rules = append(rules, router.Rule{
			Provider:  eventschema.Provider(r.Provider),
			FromModel: r.FromModel,
			ToModel:   r.ToModel,
			Quality:   r.Quality,
			Fallbacks: r.Fallbacks,
		})
	}
	return &router.Config{Rules: rules, MinQuality: o.RoutingMinQuality}
}

// PricingConfig points at an optional YAML rate file that is layered on
// top of the built-in list-price catalog (spend.TableWithOverrides).
// Use it to price newly released models before a tokenops upgrade, or
// to apply negotiated rates. Same schema as the embedded pricing.yaml.
type PricingConfig struct {
	Path string `yaml:"path"`
}

// DashboardConfig gates /dashboard + /api/* behind a shared-secret
// token. AdminToken empty → daemon mints + persists one to
// ~/.tokenops/dashboard.token on first start. Setting it explicitly
// (env-substituted via the loader) lets ops roll the secret without
// touching disk state.
type DashboardConfig struct {
	AdminToken string `yaml:"admin_token"`
}

// VendorUsageConfig wires the vendor-side usage pollers. Each provider
// has its own block because authentication, polling cadence, and the
// signal quality story differ. ClaudeCode reads the local Claude Code
// stats cache; future blocks (anthropic admin API, openai usage)
// add here as separate sub-structs.
type VendorUsageConfig struct {
	ClaudeCode      ClaudeCodeUsageConfig      `yaml:"claude_code"`
	ClaudeCodeJSONL ClaudeCodeJSONLUsageConfig `yaml:"claude_code_jsonl"`
	CodexJSONL      CodexJSONLUsageConfig      `yaml:"codex_jsonl"`
	Anthropic       AnthropicUsageConfig       `yaml:"anthropic"`
	GitHubCopilot   GitHubCopilotUsageConfig   `yaml:"github_copilot"`
	Cursor          CursorUsageConfig          `yaml:"cursor"`
	AnthropicCookie AnthropicCookieUsageConfig `yaml:"anthropic_cookie"`
}

// GitHubCopilotUsageConfig wires the api.github.com/copilot_internal/user
// poller. OAuthToken empty → poller reads it from
// ~/.config/github-copilot/apps.json (or hosts.json) — same file the
// IDE plugins use. Interval defaults to 2 minutes.
type GitHubCopilotUsageConfig struct {
	Enabled    bool          `yaml:"enabled"`
	OAuthToken string        `yaml:"oauth_token"`
	Interval   time.Duration `yaml:"interval"`
}

// CursorUsageConfig wires the cursor.com/api/usage poller. Cookie +
// UserID must be set (extract from the Cursor IDE devtools, or via a
// future state.vscdb auto-discovery). Empty means the poller stays
// idle. Interval defaults to 2 minutes.
type CursorUsageConfig struct {
	Enabled  bool          `yaml:"enabled"`
	Cookie   string        `yaml:"cookie"`
	UserID   string        `yaml:"user_id"`
	Interval time.Duration `yaml:"interval"`
}

// AnthropicCookieUsageConfig wires the claude.ai cookie-scraping
// poller. SessionKey extracted from the operator's browser (devtools
// → Application → Cookies → sessionKey). OrgID empty → poller
// resolves it via /api/organizations on first scan. Interval defaults
// to 5 minutes; Anthropic's cookie tier rate-limits aggressive
// polling and the data shifts on a 5-hour bucket cadence anyway.
type AnthropicCookieUsageConfig struct {
	Enabled    bool          `yaml:"enabled"`
	SessionKey string        `yaml:"session_key"`
	OrgID      string        `yaml:"org_id"`
	Interval   time.Duration `yaml:"interval"`
}

// CodexJSONLUsageConfig enables the Codex CLI session-log reader.
// Parses ~/.codex/sessions/<yyyy>/<mm>/<dd>/rollout-*.jsonl. Empty
// Root defaults to ~/.codex/sessions.
type CodexJSONLUsageConfig struct {
	Enabled  bool          `yaml:"enabled"`
	Root     string        `yaml:"root"`
	Interval time.Duration `yaml:"interval"`
}

// ClaudeCodeJSONLUsageConfig enables the per-turn JSONL reader that
// parses ~/.claude/projects/**/*.jsonl. This is the high-confidence
// successor to the v0.10.2 stats-cache reader (which lags by days on
// active users). Empty Root defaults to ~/.claude/projects.
type ClaudeCodeJSONLUsageConfig struct {
	Enabled  bool          `yaml:"enabled"`
	Root     string        `yaml:"root"`
	Interval time.Duration `yaml:"interval"`
}

// AnthropicUsageConfig wires the Anthropic Admin API poller. AdminKey
// must be a sk-ant-admin-* key minted in the Claude Console; without
// one the poller stays idle but the daemon still starts. Interval
// defaults to 5 minutes (the API freshness lag).
type AnthropicUsageConfig struct {
	Enabled     bool          `yaml:"enabled"`
	AdminKey    string        `yaml:"admin_key"`
	Interval    time.Duration `yaml:"interval"`
	BucketWidth string        `yaml:"bucket_width"`
}

// ClaudeCodeUsageConfig enables reading ~/.claude/stats-cache.json and
// emitting envelopes for the per-model daily token totals Claude Code
// records there. Empty Path defaults to the conventional location.
// Interval below 15s is clamped at the poller level to avoid
// hammering the file on caches that rotate frequently.
type ClaudeCodeUsageConfig struct {
	Enabled  bool          `yaml:"enabled"`
	Path     string        `yaml:"path"`
	Interval time.Duration `yaml:"interval"`
}

// ResilienceConfig wraps each provider proxy route with
// fortify's CircuitBreakerStream. Off by default; opt in to gain
// per-stream FirstByte / Idle / Total deadlines and per-provider
// circuit breakers across SSE streams. Zero-valued deadlines disable
// the corresponding watchdog (at least one must be positive when
// enabled).
type ResilienceConfig struct {
	Enabled          bool          `yaml:"enabled"`
	FirstByteTimeout time.Duration `yaml:"first_byte_timeout"`
	IdleTimeout      time.Duration `yaml:"idle_timeout"`
	TotalTimeout     time.Duration `yaml:"total_timeout"`
	// FailureThreshold is the consecutive-failure count that trips
	// the breaker for a given route. Defaults to 5 when zero.
	FailureThreshold uint32 `yaml:"failure_threshold"`
}

// RulesConfig wires the Rule Intelligence subsystem (issue #12).
// Enabled gates the /api/rules/* dashboard endpoints. Root is the
// repository the daemon scans on each request — defaults to the daemon's
// working directory when unset. RepoID is an opaque identifier prepended
// to rule SourceIDs (allows cross-repo aggregation without leaking repo
// names through telemetry).
type RulesConfig struct {
	Enabled bool   `yaml:"enabled"`
	Root    string `yaml:"root"`
	RepoID  string `yaml:"repo_id"`
}

// OTelConfig configures the optional OTLP/HTTP/JSON telemetry exporter.
// When Enabled, every envelope emitted to the bus is also forwarded to
// Endpoint. Headers (e.g. tenant tokens) are sent on every request.
type OTelConfig struct {
	Enabled        bool              `yaml:"enabled"`
	Endpoint       string            `yaml:"endpoint"`
	Headers        map[string]string `yaml:"headers"`
	ServiceName    string            `yaml:"service_name"`
	ServiceVersion string            `yaml:"service_version"`
	// Redact, when true, runs the redaction pipeline before exporting.
	// Default true; explicit false disables redaction (use with care).
	Redact *bool `yaml:"redact"`
}

// RedactEnabled reports whether redaction should be applied to OTLP
// exports. Defaults to true when unset.
func (o OTelConfig) RedactEnabled() bool {
	if o.Redact == nil {
		return true
	}
	return *o.Redact
}

// StorageConfig configures the local event store. When Enabled, the
// daemon opens a sqlite database at Path and emits PromptEvents into it
// via an async bus.
type StorageConfig struct {
	Enabled bool   `yaml:"enabled"`
	Path    string `yaml:"path"`
}

// TLSConfig configures TLS termination on the local proxy.
type TLSConfig struct {
	// Enabled toggles HTTPS. When false, the proxy serves plain HTTP.
	Enabled bool `yaml:"enabled"`
	// CertDir is the directory the auto-minted CA + leaf bundle lives in.
	// On first run TokenOps creates the bundle here; on subsequent runs
	// the same files are reused. Default ~/.tokenops/certs.
	CertDir string `yaml:"cert_dir"`
	// Hostnames are extra DNS SANs added to the leaf cert. Loopback names
	// (localhost, 127.0.0.1, ::1) are always included.
	Hostnames []string `yaml:"hostnames"`
}

// LogConfig configures the structured logger.
type LogConfig struct {
	Level  string `yaml:"level"`  // debug | info | warn | error
	Format string `yaml:"format"` // json | text
}

// ShutdownConfig configures graceful shutdown behaviour.
type ShutdownConfig struct {
	Timeout time.Duration `yaml:"timeout"`
}

// Default returns the built-in defaults. The daemon is local-first by default
// and binds to loopback so a fresh install never accidentally exposes the
// proxy on the network.
func Default() Config {
	return Config{
		Listen: "127.0.0.1:7878",
		Log: LogConfig{
			Level:  "info",
			Format: "text",
		},
		Shutdown: ShutdownConfig{
			Timeout: 15 * time.Second,
		},
	}
}

// Load resolves configuration in order of precedence: defaults, optional YAML
// file (path may be empty), and environment variables. Environment variables
// always win.
func Load(path string) (Config, error) {
	cfg := Default()

	if path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			return Config{}, fmt.Errorf("read config %q: %w", path, err)
		}
		if err := yaml.Unmarshal(data, &cfg); err != nil {
			return Config{}, fmt.Errorf("parse config %q: %w", path, err)
		}
	}

	applyEnvOverrides(&cfg)

	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// Validate checks the configuration for unrecoverable errors.
func (c Config) Validate() error {
	if c.Listen == "" {
		return errors.New("listen address must not be empty")
	}
	switch strings.ToLower(c.Log.Level) {
	case "debug", "info", "warn", "error":
	default:
		return fmt.Errorf("invalid log level %q", c.Log.Level)
	}
	switch strings.ToLower(c.Log.Format) {
	case "json", "text":
	default:
		return fmt.Errorf("invalid log format %q", c.Log.Format)
	}
	if c.Shutdown.Timeout <= 0 {
		return fmt.Errorf("shutdown.timeout must be positive, got %s", c.Shutdown.Timeout)
	}
	if c.OTel.Enabled && c.OTel.Endpoint == "" {
		return errors.New("otel.endpoint must be set when otel.enabled is true")
	}
	if c.Resilience.Enabled {
		if c.Resilience.FirstByteTimeout <= 0 && c.Resilience.IdleTimeout <= 0 && c.Resilience.TotalTimeout <= 0 {
			return errors.New("resilience.enabled requires at least one positive timeout (first_byte_timeout, idle_timeout, total_timeout)")
		}
	}
	for provider, planName := range c.Plans {
		if err := plans.Validate(planName); err != nil {
			return fmt.Errorf("plans[%s]: %w", provider, err)
		}
	}
	if q := c.Optimizer.RoutingMinQuality; q < 0 || q > 1 {
		return fmt.Errorf("optimizer.routing_min_quality must be in [0,1], got %g", q)
	}
	for i, r := range c.Optimizer.RoutingRules {
		if r.Provider == "" || r.FromModel == "" || r.ToModel == "" {
			return fmt.Errorf("optimizer.routing_rules[%d]: provider, from_model, and to_model are required", i)
		}
		if r.Quality <= 0 || r.Quality > 1 {
			return fmt.Errorf("optimizer.routing_rules[%d]: quality must be in (0,1], got %g", i, r.Quality)
		}
	}
	for i, l := range c.Coaching.ContextLimits {
		if l.WorkflowPrefix == "" {
			return fmt.Errorf("coaching.context_limits[%d]: workflow_prefix is required", i)
		}
		if l.MaxContextTokens < 0 || l.ContextGrowthLimitTokens < 0 ||
			l.MaxConsecutiveAgentLoops < 0 || l.SystemRedundancyMin < 0 {
			return fmt.Errorf("coaching.context_limits[%d]: thresholds must be non-negative", i)
		}
	}
	switch strings.ToLower(c.Mode) {
	case "", ModePassive, ModeActive:
	default:
		return fmt.Errorf("mode must be %q or %q, got %q", ModePassive, ModeActive, c.Mode)
	}
	for i, b := range c.Budgets {
		if b.Name == "" {
			return fmt.Errorf("budgets[%d]: name is required", i)
		}
		switch strings.ToLower(b.Window) {
		case string(budget.WindowDaily), string(budget.WindowWeekly), string(budget.WindowMonthly):
		default:
			return fmt.Errorf("budgets[%d]: window must be daily, weekly, or monthly, got %q", i, b.Window)
		}
		if b.LimitUSD <= 0 {
			return fmt.Errorf("budgets[%d]: limit_usd must be positive, got %g", i, b.LimitUSD)
		}
		if b.WarnAt < 0 || b.WarnAt > 1 || b.CritAt < 0 || b.CritAt > 1 {
			return fmt.Errorf("budgets[%d]: warn_at and crit_at must be in [0,1]", i)
		}
		switch strings.ToLower(b.Basis) {
		case "", budget.BasisSpend, budget.BasisEquivalent:
		default:
			return fmt.Errorf("budgets[%d]: basis must be %q or %q, got %q", i, budget.BasisSpend, budget.BasisEquivalent, b.Basis)
		}
	}
	if c.Watch.Interval < 0 {
		return fmt.Errorf("watch.interval must be non-negative, got %s", c.Watch.Interval)
	}
	return nil
}

// Blockers returns the stable, machine-readable list of subsystem gates
// that prevent the daemon from returning populated data. Order is fixed
// so callers can diff successive snapshots. Returns a non-nil empty
// slice when nothing is gated so JSON serialises as [] not null.
func (c Config) Blockers() []string {
	blockers := []string{}
	if !c.Storage.Enabled {
		blockers = append(blockers, "storage_disabled")
	}
	if !c.Rules.Enabled {
		blockers = append(blockers, "rules_disabled")
	}
	if len(c.Providers) == 0 {
		blockers = append(blockers, "providers_unconfigured")
	}
	return blockers
}

// NextActionsFor maps blockers to operator-facing remediation steps and
// deduplicates so a single `tokenops init` call surfaces once even when
// it resolves multiple blockers. Returns a non-nil empty slice when
// blockers is empty.
func NextActionsFor(blockers []string) []string {
	out := []string{}
	if len(blockers) == 0 {
		return out
	}
	seen := map[string]bool{}
	add := func(s string) {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	for _, b := range blockers {
		switch b {
		case "storage_disabled", "rules_disabled":
			add("run `tokenops init` then restart the daemon")
		case "providers_unconfigured":
			add("run `tokenops provider set <name> <url>` (e.g. `tokenops provider set anthropic https://api.anthropic.com`)")
		}
	}
	return out
}

func applyEnvOverrides(cfg *Config) {
	if v := os.Getenv("TOKENOPS_LISTEN"); v != "" {
		cfg.Listen = v
	}
	if v := os.Getenv("TOKENOPS_LOG_LEVEL"); v != "" {
		cfg.Log.Level = v
	}
	if v := os.Getenv("TOKENOPS_LOG_FORMAT"); v != "" {
		cfg.Log.Format = v
	}
	if v := os.Getenv("TOKENOPS_SHUTDOWN_TIMEOUT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			cfg.Shutdown.Timeout = d
		} else if secs, err := strconv.Atoi(v); err == nil {
			cfg.Shutdown.Timeout = time.Duration(secs) * time.Second
		}
	}
	if v := os.Getenv("TOKENOPS_TLS_ENABLED"); v != "" {
		switch strings.ToLower(v) {
		case "1", "true", "yes", "on":
			cfg.TLS.Enabled = true
		case "0", "false", "no", "off":
			cfg.TLS.Enabled = false
		}
	}
	if v := os.Getenv("TOKENOPS_TLS_CERT_DIR"); v != "" {
		cfg.TLS.CertDir = v
	}
	if v := os.Getenv("TOKENOPS_STORAGE_ENABLED"); v != "" {
		switch strings.ToLower(v) {
		case "1", "true", "yes", "on":
			cfg.Storage.Enabled = true
		case "0", "false", "no", "off":
			cfg.Storage.Enabled = false
		}
	}
	if v := os.Getenv("TOKENOPS_STORAGE_PATH"); v != "" {
		cfg.Storage.Path = v
	}
	if v := os.Getenv("TOKENOPS_OTEL_ENABLED"); v != "" {
		switch strings.ToLower(v) {
		case "1", "true", "yes", "on":
			cfg.OTel.Enabled = true
		case "0", "false", "no", "off":
			cfg.OTel.Enabled = false
		}
	}
	if v := os.Getenv("TOKENOPS_OTEL_ENDPOINT"); v != "" {
		cfg.OTel.Endpoint = v
	}
	if v := os.Getenv("TOKENOPS_OTEL_SERVICE_NAME"); v != "" {
		cfg.OTel.ServiceName = v
	}
	if v := os.Getenv("TOKENOPS_PRICING_PATH"); v != "" {
		cfg.Pricing.Path = v
	}
	for _, key := range []string{"openai", "anthropic", "gemini"} {
		envKey := "TOKENOPS_PROVIDER_" + strings.ToUpper(key) + "_URL"
		if v := os.Getenv(envKey); v != "" {
			if cfg.Providers == nil {
				cfg.Providers = make(map[string]string, 3)
			}
			cfg.Providers[key] = v
		}
		planEnvKey := "TOKENOPS_PLAN_" + strings.ToUpper(key)
		if v := os.Getenv(planEnvKey); v != "" {
			if cfg.Plans == nil {
				cfg.Plans = make(map[string]string, 3)
			}
			cfg.Plans[key] = v
		}
	}
}
