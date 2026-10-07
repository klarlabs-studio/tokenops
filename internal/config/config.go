// Package config loads TokenOps daemon configuration from a YAML file with
// environment-variable overrides. The schema is intentionally small at this
// stage; subsequent tasks (proxy-providers, optimizer, observability) extend
// it with their own sections.
package config

import (
	"fmt"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
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
	// SourcePath is the file Load read, empty when none was. It is not
	// part of the file: it lets a long-running process (the daemon) write
	// a change back to the file it was started with, not the default.
	SourcePath string `yaml:"-" json:"-"`

	// Mode selects how TokenOps helps: "passive" (analytics only —
	// observe, store, answer queries) or "active" (passive + live
	// interventions: routing rules applied to proxied traffic, budget /
	// unpriced-model watcher emitting alerts). Default passive.
	Mode   string `yaml:"mode"`
	Listen string `yaml:"listen"`
	// AllowedHosts are host names (or IPs, port optional) the daemon
	// answers to beyond loopback, the listen address, tls.hostnames and
	// the advertised mDNS name — e.g. a name a reverse proxy forwards.
	// Requests addressed to any other Host are refused with 403, which
	// is what keeps a DNS-rebinding web page off the provider routes.
	AllowedHosts []string          `yaml:"allowed_hosts,omitempty"`
	Log          LogConfig         `yaml:"log"`
	Shutdown     ShutdownConfig    `yaml:"shutdown"`
	Providers    map[string]string `yaml:"providers"`
	// Plans maps provider name → plan catalog identifier (e.g.
	// "anthropic" → "claude-max-20x"). Requests routed to a provider with
	// a configured plan are billed as plan_included (CostUSD=0) and
	// roll up to the plan's monthly quota instead — except a
	// spend-denominated plan (usage-based Enterprise), which is billed at
	// API rates; see PlanCovers. See
	// internal/contexts/spend/plans for the catalog.
	Plans map[string]string `yaml:"plans"`
	// PreferredModels maps provider name → the model you want to stay on
	// (e.g. "anthropic" → "claude-opus-5"). It acts as a ceiling: a
	// routing rule that would move you to a pricier model is refused and
	// referred to you instead of applied, with the preferred model
	// offered as the alternative. Routing DOWN to something cheaper
	// still applies automatically. Empty disables the ceiling.
	PreferredModels map[string]string `yaml:"preferred_models,omitempty"`
	// ModelPolicy rules models out of every routing decision: the
	// proxy's rules and smart routing, the coach's advice and its
	// subagent moves. Deny wins; a non-empty allow list admits only what
	// it names. A company can ship it through device management like any
	// other setting.
	ModelPolicy ModelPolicyConfig `yaml:"model_policy,omitempty"`
	// Money is the currency you pay your plans in, for showing plan cost
	// and value per plan unit in it.
	Money MoneyConfig `yaml:"money,omitempty"`
	// Statusline records whether TokenOps' line shows in Claude Code's
	// status line. init installs it by default; an uninstall sets
	// enabled to false, so a later init leaves it out until the operator
	// installs it again.
	Statusline  StatuslineConfig  `yaml:"statusline,omitempty"`
	TLS         TLSConfig         `yaml:"tls"`
	Storage     StorageConfig     `yaml:"storage"`
	Retention   RetentionConfig   `yaml:"retention,omitempty"`
	OTel        OTelConfig        `yaml:"otel"`
	Rules       RulesConfig       `yaml:"rules"`
	Resilience  ResilienceConfig  `yaml:"resilience"`
	VendorUsage VendorUsageConfig `yaml:"vendor_usage"`
	MDNS        MDNSConfig        `yaml:"mdns,omitempty"`
	// PlanLimits carries the per-provider figures only the operator can
	// supply, keyed by provider name. Spend-denominated plans
	// (usage-based Enterprise) have no vendor-published cap, so their
	// denominator comes from the org's own console.
	PlanLimits map[string]PlanLimit `yaml:"plan_limits,omitempty"`
	Dashboard  DashboardConfig      `yaml:"dashboard"`
	Pricing    PricingConfig        `yaml:"pricing"`
	Optimizer  OptimizerConfig      `yaml:"optimizer"`
	Coaching   CoachingConfig       `yaml:"coaching"`
	// Coach is the coach's autonomy and verbosity (ADR 0006). Empty keeps
	// the behaviour of the coaching and smart-routing keys.
	Coach   CoachConfig    `yaml:"coach,omitempty"`
	Budgets []BudgetConfig `yaml:"budgets"`
	Watch   WatchConfig    `yaml:"watch"`
}

// ActiveMode reports whether interventions (live routing, spend
// watcher) are enabled. Empty Mode means passive.
func (c Config) ActiveMode() bool { return strings.EqualFold(c.Mode, ModeActive) }

// ParseMode normalises an operating mode a caller asked for, refusing
// anything but passive or active. The terminal's `mode` and the MCP tool
// share it, so neither can write a mode the other would refuse.
func ParseMode(s string) (string, error) {
	switch m := strings.ToLower(strings.TrimSpace(s)); m {
	case ModePassive, ModeActive:
		return m, nil
	default:
		return "", fmt.Errorf("mode must be %q or %q, got %q", ModePassive, ModeActive, s)
	}
}

// StatuslineConfig is the operator's choice about TokenOps' status line.
type StatuslineConfig struct {
	// Enabled is nil until the operator chooses; nil means on.
	Enabled *bool `yaml:"enabled,omitempty"`
}

// StatuslineWanted reports whether init should install the status line:
// unless the operator turned it off.
func (c Config) StatuslineWanted() bool {
	return c.Statusline.Enabled == nil || *c.Statusline.Enabled
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
		if err := checkRenamedKeys(data, path); err != nil {
			return Config{}, err
		}
	}

	applyEnvOverrides(&cfg)
	expandHomePaths(&cfg)

	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	cfg.SourcePath = path
	return cfg, nil
}
