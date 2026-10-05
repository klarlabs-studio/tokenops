package mcp

import (
	"context"
	"errors"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/spend/spend"

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
	// Spend prices requests for spend-denominated plans; nil counts only
	// measured cost.
	Spend *spend.Engine
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
	return r.store.ReadEvents(ctx, t, since)
}

// planHeadroomResult and sessionBudgetResult are the capability's wire
// payloads, shared with the daemon API (ADR 0010 §4).
type (
	planHeadroomResult  = headroom.HeadroomPayload
	sessionBudgetResult = headroom.BudgetPayload
)

// RegisterPlanTools mounts tokenops_glance (view=headroom) on s. Returns an
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
		Description("Predict the operator's rate-limit headroom for the current MCP session. Returns plan_name, window_consumed, window_pct, recent_rate_per_hour, will_hit_cap_within, headroom_until_cap, confidence (low|medium|high), and recommended_action (continue|slow_down|switch_model|wait_for_reset). Designed for Claude Code / Cursor agents to call before starting a long task. A plan with no rate-limit window (e.g. spend-billed claude-enterprise) has no session budget; it is explained in notes and its spend is in tokenops_glance (view=headroom).").
		Handler(func(ctx context.Context, _ emptyInput) (string, error) {
			return sessionBudget(ctx, d)
		})

	s.Tool("tokenops_plan_headroom").
		Description("Return month-to-date consumption + overage risk for every configured subscription plan (Claude Max, ChatGPT Plus, Copilot, Cursor, etc.). Returns a structured `{error, hint}` payload when plans or storage are not configured.").
		OutputSchema(planHeadroomResult{}).
		Handler(func(ctx context.Context, _ emptyInput) (*planHeadroomResult, error) {
			return planHeadroom(ctx, d)
		})
	registerResourceGlanceTool(s, d)
	return nil
}

func sessionBudget(ctx context.Context, d PlanDeps) (string, error) {
	result, err := sessionBudgetData(ctx, d)
	if err != nil {
		return "", err
	}
	if result.Error != "" {
		return jsonString(map[string]string{"error": result.Error, "hint": result.Hint}), nil
	}
	if len(result.Budgets) == 0 {
		return jsonString(result), nil
	}
	b := result.Budgets[0]
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
		Windows:           b.Windows,
	}
	return markdownPayload(renderBudgetSummary(row), result), nil
}

func sessionBudgetData(ctx context.Context, d PlanDeps) (*sessionBudgetResult, error) {
	res, err := headroom.SessionBudgets(ctx, d.headroomDeps(), time.Now().UTC())
	if err != nil {
		return nil, err
	}
	return res.Payload(), nil
}

// headroomDeps is what the headroom capability needs from this server. A
// nil store stays a nil reader, which the capability answers as storage
// disabled.
func (d PlanDeps) headroomDeps() headroom.Deps {
	deps := headroom.Deps{Config: d.activeConfig()}
	if d.Store != nil {
		deps.Reader = planStoreReader{store: d.Store}
	}
	if d.Spend != nil {
		deps.Price = d.Spend.ComputeAt
	}
	return deps
}

// planHeadroom is one implementation, shared with `tokenops plan headroom`
// and the daemon API. The CLI and this tool used to be separate copies of
// the loop, which is how the CLI kept the unsorted-provider bug after it
// was fixed here.
func planHeadroom(ctx context.Context, d PlanDeps) (*planHeadroomResult, error) {
	computed, err := headroom.Compute(ctx, d.headroomDeps(), time.Now().UTC())
	if err != nil {
		return nil, err
	}
	return computed.Payload(), nil
}
