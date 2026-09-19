package mcp

import (
	"context"
	"errors"
	"os"
	"strings"
	"time"

	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/claudeusagemeter"
)

// SetupDeps wires the tools that bind a plan and connect the Claude usage
// meter — the two steps a Claude Enterprise user needs, reachable from an
// agent as well as from `tokenops plan set` and `tokenops vendor-usage
// setup`.
type SetupDeps struct {
	ConfigPath string
	// ApplyConfig makes a written config take effect; see applyConfig.
	ApplyConfig func() string
	// Getenv reads the session key's environment variable. nil uses
	// os.Getenv; tests inject.
	Getenv func(string) string
	// MeterBaseURL overrides claude.ai for tests.
	MeterBaseURL string
}

func (d SetupDeps) path() (string, error) {
	if d.ConfigPath != "" {
		return d.ConfigPath, nil
	}
	return config.DefaultPath()
}

func (d SetupDeps) getenv(k string) string {
	if d.Getenv != nil {
		return d.Getenv(k)
	}
	return os.Getenv(k)
}

type planSetInput struct {
	Provider      string  `json:"provider,omitempty" jsonschema:"description=Provider to bind, e.g. anthropic. Omit to list the current bindings."`
	Plan          string  `json:"plan,omitempty" jsonschema:"description=Plan name from the catalog, e.g. claude-max-20x or claude-enterprise."`
	SpendLimitUSD float64 `json:"spend_limit_usd,omitempty" jsonschema:"description=Spend limit in USD for a plan billed at API rates (claude-enterprise). Not needed when the Claude usage meter is connected: Anthropic reports it."`
	LimitWindow   string  `json:"limit_window,omitempty" jsonschema:"enum=monthly,enum=weekly,enum=daily,description=Period the spend limit covers. Default monthly."`
	RateFactor    float64 `json:"rate_factor,omitempty" jsonschema:"description=Scale estimated spend to a negotiated rate (0.8 = 20% off list)."`
	Clear         bool    `json:"clear,omitempty" jsonschema:"description=Remove the provider's plan binding."`
}

type meterSetupInput struct {
	Org string `json:"org,omitempty" jsonschema:"description=Organization to meter, by name or UUID. Omit to use the first one that reports usage."`
}

// meterKeyEnv is where the meter tool reads the session key. It never takes
// the key as an argument: a claude.ai session cookie logs in as the user,
// and a tool argument is written into the agent's transcript.
const meterKeyEnv = "TOKENOPS_CLAUDE_USAGE_METER_SESSION_KEY"

// RegisterSetupTools adds tokenops_plan_set and tokenops_vendor_usage_setup.
func RegisterSetupTools(s *Server, d SetupDeps) error {
	s.Tool("tokenops_plan_set").
		Description("Bind a provider to a subscription plan (the MCP twin of `tokenops plan set`), or list the bindings when provider is omitted. claude-enterprise is billed at API rates: its limit comes from the Claude usage meter when connected, otherwise pass spend_limit_usd. Restarts the supervised daemon so the change is live.").
		Handler(func(_ context.Context, in planSetInput) (string, error) {
			path, err := d.path()
			if err != nil {
				return "", err
			}
			cfg, err := config.ReadMutable(path)
			if err != nil {
				return "", err
			}
			provider := strings.TrimSpace(in.Provider)
			if provider == "" {
				return jsonString(map[string]any{"plans": cfg.Plans, "plan_limits": cfg.PlanLimits, "config": path}), nil
			}
			resp := map[string]any{"provider": provider, "config": path}
			if in.Clear {
				delete(cfg.Plans, provider)
				resp["cleared"] = true
			} else {
				b, err := cfg.BindPlan(provider, strings.TrimSpace(in.Plan), config.PlanLimit{
					SpendLimitUSD: in.SpendLimitUSD, Window: in.LimitWindow, RateFactor: in.RateFactor,
				})
				if err != nil {
					return "", err
				}
				resp["plan"] = b.Plan
				if b.Previous != "" && b.Previous != b.Plan {
					resp["previous"] = b.Previous
				}
				if b.RenamedFrom != "" {
					resp["renamed_from"] = b.RenamedFrom
				}
			}
			if err := config.WriteMutable(path, cfg); err != nil {
				return "", err
			}
			resp["note"] = applyConfig(d.ApplyConfig)
			return jsonString(resp), nil
		})

	s.Tool("tokenops_vendor_usage_setup").
		Description("Connect the Claude usage meter (the MCP twin of `tokenops vendor-usage setup claude-usage-meter`): verifies the claude.ai session key with Anthropic, picks the organization, reports what Anthropic shows — 5-hour/7-day utilization, or Enterprise spend against your limit — and enables it. The key is read from the " + meterKeyEnv + " environment variable or existing config, never from a tool argument: never ask the user to paste it into the chat.").
		Handler(func(ctx context.Context, in meterSetupInput) (string, error) {
			path, err := d.path()
			if err != nil {
				return "", err
			}
			cfg, err := config.ReadMutable(path)
			if err != nil {
				return "", err
			}
			key := strings.TrimSpace(d.getenv(meterKeyEnv))
			if key == "" {
				key = cfg.VendorUsage.ClaudeUsageMeter.SessionKey
			}
			if key == "" {
				return jsonString(map[string]any{
					"error": "session_key_missing",
					"hint": "the session key is a claude.ai login and must not go through this chat. " +
						"Ask the user to run `tokenops vendor-usage setup claude-usage-meter` in a terminal, " +
						"which reads it without echoing it — or to set " + meterKeyEnv + " for this MCP server and call this tool again",
				}), nil
			}
			client := claudeusagemeter.NewClient(key)
			if d.MeterBaseURL != "" {
				client.BaseURL = d.MeterBaseURL
			}
			ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
			defer cancel()
			conn, err := claudeusagemeter.Connect(ctx, client, in.Org)
			if err != nil {
				if errors.Is(err, claudeusagemeter.ErrUnauthorized) {
					return jsonString(map[string]any{
						"error": "session_key_rejected",
						"hint":  "Anthropic rejected the session key — cookies rotate every few weeks; the user needs a fresh one. Nothing was written.",
					}), nil
				}
				return "", err
			}
			cfg.VendorUsage.ClaudeUsageMeter.Enabled = true
			cfg.VendorUsage.ClaudeUsageMeter.SessionKey = key
			cfg.VendorUsage.ClaudeUsageMeter.OrgID = conn.Org.UUID
			if err := config.WriteMutable(path, cfg); err != nil {
				return "", err
			}
			orgs := make([]string, 0, len(conn.Orgs))
			for _, o := range conn.Orgs {
				orgs = append(orgs, o.Name)
			}
			resp := map[string]any{
				"organization":  conn.Org.Name,
				"organizations": orgs,
				"reports":       conn.Usage.Summary(),
				"config":        path,
				"note":          applyConfig(d.ApplyConfig),
			}
			if len(conn.Usage.Unrecognised) > 0 {
				resp["unreadable"] = conn.Usage.Unrecognised
			}
			return jsonString(resp), nil
		})
	return nil
}
