package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/headroom"
	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/contexts/spend/plans"
	"go.klarlabs.de/tokenops/internal/contexts/spend/session"
	"go.klarlabs.de/tokenops/internal/storage/sqlite"
	"go.klarlabs.de/tokenops/pkg/eventschema"
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

// CountBySource satisfies headroom.Reader, so the capability can be
// handed one port instead of a reader plus a loose function value.
func (r planStoreReader) CountBySource(ctx context.Context, since, until time.Time) (map[string]int64, error) {
	return r.store.CountBySource(ctx, since, until)
}

// classifySignalFromStore reads CountBySource over the headroom window
// and feeds proxy + mcp-session counts into the domain classifier so
// every response carries an honest trust level. Vendor /usage isn't
// wired yet so VendorAPIWired stays false.
func classifySignalFromStore(ctx context.Context, store *sqlite.Store, provider string, since, until time.Time) (plans.SignalInputs, error) {
	if store == nil {
		return plans.SignalInputs{}, nil
	}
	counts, err := store.CountBySource(ctx, since, until)
	if err != nil {
		return plans.SignalInputs{}, err
	}
	return plans.SignalFromCounts(counts, provider), nil
}

func (r planStoreReader) ReadEvents(ctx context.Context, t eventschema.EventType, since time.Time) ([]*eventschema.Envelope, error) {
	return r.store.Query(ctx, sqlite.Filter{Type: t, Since: since, Limit: 100_000})
}

// planHeadroomResult is the typed payload for tokenops_plan_headroom. On
// the happy path Reports (+ optional DataWarning) is populated; the
// unconfigured / storage-disabled paths set Error + Hint instead.
type planHeadroomResult struct {
	Reports []plans.HeadroomReport `json:"reports,omitempty"`
	// Notes names plans that could not be reported on. An unknown plan
	// name used to be skipped silently, so an operator's typo produced a
	// plan that reported nothing and said nothing, forever.
	Notes       []string     `json:"notes,omitempty"`
	DataWarning *DataWarning `json:"data_warning,omitempty"`
	Error       string       `json:"error,omitempty"`
	Hint        string       `json:"hint,omitempty"`
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
		Description("Predict the operator's rate-limit headroom for the current MCP session. Returns plan_name, window_consumed, window_pct, recent_rate_per_hour, will_hit_cap_within, headroom_until_cap, confidence (low|medium|high), and recommended_action (continue|slow_down|switch_model|wait_for_reset). Designed for Claude Code / Cursor agents to call before starting a long task. A plan with no rate-limit window (e.g. spend-billed claude-enterprise) has no session budget; it is explained in notes and its spend is in tokenops_plan_headroom.").
		Handler(func(ctx context.Context, _ emptyInput) (string, error) {
			return sessionBudget(ctx, d)
		})

	s.Tool("tokenops_plan_headroom").
		Description("Return month-to-date consumption + overage risk for every configured subscription plan (Claude Max, ChatGPT Plus, Copilot, Cursor, etc.). Returns a structured `{error, hint}` payload when plans or storage are not configured.").
		OutputSchema(planHeadroomResult{}).
		Handler(func(ctx context.Context, _ emptyInput) (*planHeadroomResult, error) {
			return planHeadroom(ctx, d)
		})
	return nil
}

// plansUnconfiguredHint names both ways to bind a plan. Config hot-reloads
// through ConfigGetter, so the "then reload your MCP server" this used to
// end with was a step that changed nothing — and the agent reading the
// hint can bind the plan itself.
const plansUnconfiguredHint = "bind a plan with tokenops_plan_set, or `tokenops plan set <provider> <plan>` " +
	"(e.g. `tokenops plan set anthropic claude-max-20x`); the change is picked up without a restart"

// sortedProviders returns the configured providers in a stable order.
// Ranging the map directly made "the first budget" — the one rendered as
// markdown — a different plan from call to call.
func sortedProviders(bindings map[string]string) []string {
	out := make([]string, 0, len(bindings))
	for p := range bindings {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// windowlessPlanNote explains a plan that has no session budget to report.
// Skipping it silently gave an Enterprise operator {"budgets":[]}, which
// reads the same as "nothing configured".
func windowlessPlanNote(provider, planName string, p plans.Plan) string {
	if p.SpendDenominated {
		return fmt.Sprintf("%s: %s is billed by spend and has no rate-limit window, so there is no session budget; "+
			"call tokenops_plan_headroom for spend against the limit", provider, planName)
	}
	return fmt.Sprintf("%s: %s has no rolling rate-limit window, so there is no session budget; "+
		"call tokenops_plan_headroom for month-to-date consumption", provider, planName)
}

func sessionBudget(ctx context.Context, d PlanDeps) (string, error) {
	cfg := d.activeConfig()
	if cfg == nil || len(cfg.Plans) == 0 {
		return jsonString(map[string]string{
			"error": "plans_unconfigured",
			"hint":  plansUnconfiguredHint,
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
	var notes []string
	for _, provider := range sortedProviders(cfg.Plans) {
		planName := cfg.Plans[provider]
		p, ok := plans.Lookup(planName)
		if !ok {
			notes = append(notes, fmt.Sprintf("%s: %s is not a known plan", provider, planName))
			continue
		}
		if p.RateLimitWindow <= 0 {
			notes = append(notes, windowlessPlanNote(provider, planName, p))
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
		signal, err := classifySignalFromStore(ctx, d.Store, provider, now.Add(-p.RateLimitWindow), now)
		if err != nil {
			return "", fmt.Errorf("signal[%s]: %w", provider, err)
		}
		budget, err := plans.ComputeSessionBudget(planName, plans.SessionBudgetInputs{
			WindowMessages:  windowCons.MessagesInWindow,
			WindowStartedAt: windowCons.FirstActivityAt,
			RecentMessages:  recentCons.MessagesInWindow,
			RecentWindow:    30 * time.Minute,
			Signal:          signal,
			Now:             now,
			// Prefer the vendor's own reported quota when a snapshot
			// source (Claude usage meter / Codex rate_limits / Copilot) has
			// emitted one; falls back to the message-count heuristic.
			Authoritative: plans.LatestAuthoritativeWindow(ctx, reader, eventschema.Provider(provider), p, now),
		})
		if err != nil {
			return "", fmt.Errorf("budget[%s]: %w", provider, err)
		}
		budgets = append(budgets, budget)
	}
	payload := map[string]any{"budgets": budgets}
	if len(notes) > 0 {
		payload["notes"] = notes
	}
	if warn, err := maybeDataWarning(ctx, d.Store, time.Time{}, now); err == nil && warn != nil {
		payload["data_warning"] = warn
	}
	// Render the first budget — first in provider order, so the same
	// plan every call — as a markdown table for clients that surface
	// text content visually (Desktop, Code, Cursor). The JSON appendix
	// stays intact for agents.
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

func planHeadroom(ctx context.Context, d PlanDeps) (*planHeadroomResult, error) {
	cfg := d.activeConfig()
	if cfg == nil || len(cfg.Plans) == 0 {
		return &planHeadroomResult{
			Error: "plans_unconfigured",
			Hint:  plansUnconfiguredHint,
		}, nil
	}
	if d.Store == nil {
		return &planHeadroomResult{
			Error: "storage_disabled",
			Hint:  "run `tokenops init` then restart the daemon",
		}, nil
	}
	now := time.Now().UTC()
	// One implementation, shared with `tokenops plan headroom`. The two
	// used to be separate copies of this loop, which is how the CLI kept
	// the unsorted-provider bug after it was fixed here.
	computed, err := headroom.Compute(ctx, headroom.Deps{
		Config: cfg,
		Reader: planStoreReader{store: d.Store},
	}, now)
	if err != nil {
		return nil, err
	}
	res := &planHeadroomResult{Reports: computed.Reports, Notes: computed.Notes}
	if warn, err := maybeDataWarning(ctx, d.Store, time.Time{}, now); err == nil && warn != nil {
		res.DataWarning = warn
	}
	return res, nil
}
