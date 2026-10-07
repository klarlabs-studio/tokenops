package mcp

import (
	"context"
	"errors"
	"os"
	"strings"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/actions"
	"go.klarlabs.de/tokenops/internal/capability/usagemeter"

	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/infra/planhistory"
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
	// BrowserCookie reads the claude.ai session, with its clearance,
	// from a local browser. macOS asks the operator to allow the keychain
	// read, which is the consent this tool cannot ask for itself. nil
	// skips the browser.
	BrowserCookie func(ctx context.Context) (usagemeter.Session, error)
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
	Price         float64 `json:"price,omitempty" jsonschema:"description=What the user pays per month for this plan as on their bill (regional price, tax included). Only when the user states it."`
	Currency      string  `json:"currency,omitempty" jsonschema:"description=ISO 4217 code of price, e.g. EUR. Default: the configured money.currency, else USD."`
	Since         string  `json:"since,omitempty" jsonschema:"description=Date the plan took effect (2026-09-01) when the user was on it before today. Re-marks the provider's usage recorded as billed since then as plan-covered. Only when the user says so."`
}

type meterSetupInput struct {
	Org string `json:"org,omitempty" jsonschema:"description=Organization to meter, by name or UUID. Omit to use the first one that reports usage."`
}

// meterKeyEnv is where the meter tool reads the session key. It never takes
// the key as an argument: a claude.ai session cookie logs in as the user,
// and a tool argument is written into the agent's transcript.
const meterKeyEnv = "TOKENOPS_CLAUDE_USAGE_METER_SESSION_KEY"

// RegisterSetupTools adds tokenops_configure (setting=plan) and tokenops_configure (setting=usage_meter).
func RegisterSetupTools(s *Server, d SetupDeps) error {
	s.Tool("tokenops_plan_set").
		Description("Bind a provider to a subscription plan (the MCP twin of `tokenops plan set`), or list the bindings and the recorded plan switches when provider is omitted. Pass since only when the user says they have been on the plan since that date: it re-marks usage recorded as billed since then as plan-covered. claude-enterprise is billed at API rates: its limit comes from the Claude usage meter when connected, otherwise pass spend_limit_usd. Restarts the supervised daemon so the change is live.").
		Handler(func(_ context.Context, in planSetInput) (string, error) {
			path, err := d.path()
			if err != nil {
				return "", inputError(err)
			}
			cfg, err := config.ReadMutable(path)
			if err != nil {
				return "", inputError(err)
			}
			provider := strings.TrimSpace(in.Provider)
			if provider == "" {
				listing := map[string]any{"plans": cfg.Plans, "plan_limits": cfg.PlanLimits, "config": path}
				if file, err := planhistory.Default(); err == nil {
					if h, err := file.Load(); err == nil && len(h) > 0 {
						listing["history"] = h
					}
				}
				return jsonString(listing), nil
			}
			change, err := actions.SetPlan(context.Background(), path, actions.PlanRequest{
				Provider: provider, Plan: in.Plan, SpendLimitUSD: in.SpendLimitUSD, LimitWindow: in.LimitWindow,
				RateFactor: in.RateFactor, Clear: in.Clear, Price: in.Price, Currency: in.Currency,
				Since: in.Since, Actor: "mcp",
			}, time.Now().UTC())
			if err != nil {
				return "", actionError(err)
			}
			return withNote(change, applyConfig(d.ApplyConfig)), nil
		})

	s.Tool("tokenops_vendor_usage_setup").
		Description("Connect Claude subscription telemetry (the MCP twin of `tokenops vendor-usage setup claude-subscription`): verifies the claude.ai session key with Anthropic, picks the organization, reports the subscription windows Anthropic shows, or Enterprise spend against your limit, and enables it. The key is read from the " + meterKeyEnv + " environment variable or existing config, never from a tool argument: never ask the user to paste it into the chat.").
		Handler(func(ctx context.Context, in meterSetupInput) (string, error) {
			path, err := d.path()
			if err != nil {
				return "", inputError(err)
			}
			cfg, err := config.ReadMutable(path)
			if err != nil {
				return "", inputError(err)
			}
			session := usagemeter.Session{Key: strings.TrimSpace(d.getenv(meterKeyEnv))}
			if session.Key == "" {
				// The stored session, with what travels with it: setting
				// only the key would drop its clearance and its browser.
				m := cfg.VendorUsage.ClaudeUsageMeter
				session = usagemeter.Session{Key: m.SessionKey, Clearance: m.Clearance, UserAgent: m.UserAgent,
					BrowserHeaders: m.BrowserHeaders, BrowserCookies: m.BrowserCookies}
				if m.FromBrowser {
					session.Browser = m.Browser
				}
			}
			if session.Key == "" && d.BrowserCookie != nil {
				// The operator is signed in to claude.ai in a browser that
				// already holds this cookie; macOS asks them to allow the
				// read. Nothing is typed, and the key never enters the
				// conversation.
				if s, err := d.BrowserCookie(ctx); err == nil && strings.TrimSpace(s.Key) != "" {
					session = s
					session.Key = strings.TrimSpace(s.Key)
				}
			}
			if session.Key == "" {
				return jsonString(map[string]any{
					"error": "session_key_missing",
					"hint": "no claude.ai session was found in a local browser, and the key is a login that must not go through this chat. " +
						"Ask the user to run `tokenops vendor-usage setup claude-subscription` in a terminal, " +
						"which reads it without echoing it — or to set " + meterKeyEnv + " for this MCP server and call this tool again",
				}), nil
			}
			ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
			defer cancel()
			conn, err := usagemeter.Verify(ctx, session, in.Org, d.MeterBaseURL)
			if err != nil {
				switch {
				case errors.Is(err, usagemeter.ErrUnauthorized):
					return jsonString(map[string]any{
						"error": "session_key_rejected",
						"hint":  "Anthropic rejected the session key — cookies rotate every few weeks; the user needs a fresh one. Nothing was written.",
					}), nil
				case errors.Is(err, usagemeter.ErrBotCheck):
					return jsonString(map[string]any{
						"error": "bot_check",
						"hint": "claude.ai's bot check refused the request. Ask the user to open claude.ai in their browser once, then call this tool again, " +
							"or to run `tokenops vendor-usage setup claude-subscription --paste-request` in a terminal. Nothing was written.",
					}), nil
				}
				return "", inputError(err)
			}
			usagemeter.Apply(&cfg, session, conn)
			if err := config.WriteMutable(path, cfg); err != nil {
				return "", inputError(err)
			}
			from := session.Browser
			orgs := make([]string, 0, len(conn.Orgs))
			for _, o := range conn.Orgs {
				orgs = append(orgs, o.Name)
			}
			resp := map[string]any{
				"read_from":     from,
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
