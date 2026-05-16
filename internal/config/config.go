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

	"github.com/felixgeelhaar/tokenops/internal/contexts/spend/plans"
)

// Config is the root daemon configuration.
type Config struct {
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
