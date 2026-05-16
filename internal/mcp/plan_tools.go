package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/felixgeelhaar/tokenops/internal/config"
	"github.com/felixgeelhaar/tokenops/internal/contexts/spend/plans"
	"github.com/felixgeelhaar/tokenops/internal/contexts/spend/session"
	"github.com/felixgeelhaar/tokenops/internal/storage/sqlite"
	"github.com/felixgeelhaar/tokenops/pkg/eventschema"
)

// PlanDeps wires the plan-headroom MCP tool. Config supplies the
// configured plans map; Store backs consumption queries; Tracker
// records the MCP-side activity ping so headroom math reflects
// real usage even without a proxy. ConfigGetter, when set, takes
// precedence over Config so callers can hot-reload the snapshot
// without re-registering tools.
type PlanDeps struct {
	Config       *config.Config
	ConfigGetter func() *config.Config
	Store        *sqlite.Store
	Tracker      *session.Tracker
	Provider     eventschema.Provider
}

// activeConfig returns the live Config snapshot: prefers ConfigGetter
// (hot-reload aware) and falls back to the static Config pointer for
// callers that don't wire a watcher.
func (d PlanDeps) activeConfig() *config.Config {
	if d.ConfigGetter != nil {
		return d.ConfigGetter()
	}
	return d.Config
}

// planStoreReader adapts *sqlite.Store to plans.EventReader without
// dragging the sqlite dependency into the domain package.
type planStoreReader struct{ store *sqlite.Store }

// classifySignalFromStore reads CountBySource over the headroom window
// and feeds proxy + mcp-session counts into the domain classifier so
// every response carries an honest trust level. Vendor /usage isn't
// wired yet so VendorAPIWired stays false.
func classifySignalFromStore(ctx context.Context, store *sqlite.Store, since, until time.Time) (plans.SignalInputs, error) {
	if store == nil {
		return plans.SignalInputs{}, nil
	}
	counts, err := store.CountBySource(ctx, since, until)
	if err != nil {
		return plans.SignalInputs{}, err
	}
	return plans.SignalInputs{
		ProxyEventsInWindow:     counts["proxy"],
		MCPPingsInWindow:        counts["mcp-session"],
		ClaudeCodeCacheInWindow: counts["claude-code-stats-cache"],
		ClaudeCodeJSONLInWindow: counts["claude-code-jsonl"],
		CodexJSONLInWindow:      counts["codex-jsonl"],
		CopilotInWindow:         counts["github-copilot"],
		CursorInWindow:          counts["cursor-web"],
		AnthropicCookieInWindow: counts["anthropic-cookie"],
		VendorAPIWired:          counts["vendor-usage-anthropic"] > 0,
	}, nil
}

func (r planStoreReader) ReadEvents(ctx context.Context, t eventschema.EventType, since time.Time) ([]*eventschema.Envelope, error) {
	return r.store.Query(ctx, sqlite.Filter{Type: t, Since: since, Limit: 100_000})
}

// RegisterPlanTools mounts tokenops_plan_headroom on s. Returns an
// error when deps are incomplete so callers can surface the
// misconfiguration via the structured-error contract instead of a
// silent zero-data response.
func RegisterPlanTools(s *Server, d PlanDeps) error {
	if s == nil {
		return errors.New("mcp: server must not be nil")
	}
	// Per-tool session-ping recording moved to SessionMiddleware so
	// every tokenops_* invocation lands in the session counter, not
	// just the two plan tools.
	s.Tool("tokenops_session_budget").
		Description("Predict the operator's rate-limit headroom for the current MCP session. Returns plan_name, window_consumed, window_pct, recent_rate_per_hour, will_hit_cap_within, headroom_until_cap, confidence (low|medium|high), and recommended_action (continue|slow_down|switch_model|wait_for_reset). Designed for Claude Code / Cursor agents to call before starting a long task.").
		Handler(func(ctx context.Context, _ emptyInput) (string, error) {
			return sessionBudget(ctx, d)
		})

	s.Tool("tokenops_plan_headroom").
		Description("Return month-to-date consumption + overage risk for every configured subscription plan (Claude Max, ChatGPT Plus, Copilot, Cursor, etc.). Returns a structured `{error, hint}` payload when plans or storage are not configured.").
		Handler(func(ctx context.Context, _ emptyInput) (string, error) {
			return planHeadroom(ctx, d)
		})
	return nil
}

func sessionBudget(ctx context.Context, d PlanDeps) (string, error) {
	cfg := d.activeConfig()
	if cfg == nil || len(cfg.Plans) == 0 {
		return jsonString(map[string]string{
			"error": "plans_unconfigured",
			"hint":  "run `tokenops plan set <provider> <plan>` (e.g. `tokenops plan set anthropic claude-max-20x`), then reload your MCP server",
		}), nil
	}
	if d.Store == nil {
		return jsonString(map[string]string{
			"error": "storage_disabled",
			"hint":  "run `tokenops init` then restart the daemon",
		}), nil
	}
	reader := planStoreReader{store: d.Store}
	now := time.Now().UTC()
	budgets := make([]plans.SessionBudget, 0, len(cfg.Plans))
	for provider, planName := range cfg.Plans {
		p, ok := plans.Lookup(planName)
		if !ok || p.RateLimitWindow <= 0 {
			continue
		}
		windowCons, err := plans.ConsumptionInWindow(ctx, reader, provider, now, p.RateLimitWindow)
		if err != nil {
			return "", fmt.Errorf("window[%s]: %w", provider, err)
		}
		recentCons, err := plans.ConsumptionInWindow(ctx, reader, provider, now, 30*time.Minute)
		if err != nil {
			return "", fmt.Errorf("recent[%s]: %w", provider, err)
		}
		signal, err := classifySignalFromStore(ctx, d.Store, now.Add(-p.RateLimitWindow), now)
		if err != nil {
			return "", fmt.Errorf("signal[%s]: %w", provider, err)
		}
		budget, err := plans.ComputeSessionBudget(planName, plans.SessionBudgetInputs{
			WindowMessages: windowCons.MessagesInWindow,
			RecentMessages: recentCons.MessagesInWindow,
			RecentWindow:   30 * time.Minute,
			Signal:         signal,
			Now:            now,
		})
		if err != nil {
			return "", fmt.Errorf("budget[%s]: %w", provider, err)
		}
		budgets = append(budgets, budget)
	}
	payload := map[string]any{"budgets": budgets}
	if warn, err := maybeDataWarning(ctx, d.Store, time.Time{}, now); err == nil && warn != nil {
		payload["data_warning"] = warn
	}
	// Render the first budget as a markdown table for clients that
	// surface text content visually (Desktop, Code, Cursor). The
	// JSON appendix stays intact for agents.
	if len(budgets) == 0 {
		out, err := json.MarshalIndent(payload, "", "  ")
		if err != nil {
			return "", err
		}
		return string(out), nil
	}
	b := budgets[0]
	row := budgetSummaryRow{
		Display:           b.Display,
		WindowConsumed:    b.WindowConsumed,
		WindowCap:         b.WindowCap,
		WindowUnit:        "messages",
		WindowPct:         b.WindowPct,
		WindowResetsIn:    b.WindowResetsIn,
		WillHitCapWithin:  b.WillHitCapWithin,
		RecentRatePerHour: b.RecentRatePerHour,
		Confidence:        b.Confidence,
		RecommendedAction: b.RecommendedAction,
		SignalLevel:       b.SignalQuality.Level,
		SignalCaveat:      b.SignalQuality.Caveat,
		Note:              b.Note,
	}
	return markdownPayload(renderBudgetSummary(row), payload), nil
}

func planHeadroom(ctx context.Context, d PlanDeps) (string, error) {
	cfg := d.activeConfig()
	if cfg == nil || len(cfg.Plans) == 0 {
		return jsonString(map[string]string{
			"error": "plans_unconfigured",
			"hint":  "run `tokenops plan set <provider> <plan>` (e.g. `tokenops plan set anthropic claude-max-20x`), then reload your MCP server",
		}), nil
	}
	if d.Store == nil {
		return jsonString(map[string]string{
			"error": "storage_disabled",
			"hint":  "run `tokenops init` then restart the daemon",
		}), nil
	}
	reader := planStoreReader{store: d.Store}
	now := time.Now().UTC()
	reports := make([]plans.HeadroomReport, 0, len(cfg.Plans))
	for provider, planName := range cfg.Plans {
		cons, err := plans.ConsumptionFor(ctx, reader, provider, now)
		if err != nil {
			return "", fmt.Errorf("consumption[%s]: %w", provider, err)
		}
		inputs := plans.HeadroomInputs{
			ConsumedTokens: cons.ConsumedTokens,
			Last7DayTokens: cons.Last7DayTokens,
			Now:            now,
		}
		if p, ok := plans.Lookup(planName); ok && p.RateLimitWindow > 0 {
			win, err := plans.ConsumptionInWindow(ctx, reader, provider, now, p.RateLimitWindow)
			if err != nil {
				return "", fmt.Errorf("window[%s]: %w", provider, err)
			}
			inputs.WindowMessages = win.MessagesInWindow
			signal, err := classifySignalFromStore(ctx, d.Store, now.Add(-p.RateLimitWindow), now)
			if err != nil {
				return "", fmt.Errorf("signal[%s]: %w", provider, err)
			}
			inputs.Signal = signal
		}
		report, err := plans.ComputeHeadroom(planName, inputs)
		if err != nil {
			return "", fmt.Errorf("headroom[%s]: %w", provider, err)
		}
		reports = append(reports, report)
	}
	payload := map[string]any{"reports": reports}
	if warn, err := maybeDataWarning(ctx, d.Store, time.Time{}, now); err == nil && warn != nil {
		payload["data_warning"] = warn
	}
	out, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return "", err
	}
	return string(out), nil
}
