package mcp

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/actions"
	"go.klarlabs.de/tokenops/internal/capability/headroom"
	"go.klarlabs.de/tokenops/internal/capability/spending"
	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/infra/observedlimits"
	"go.klarlabs.de/tokenops/internal/infra/planhistory"
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
	Tracker      *headroom.SessionTracker
	Provider     eventschema.Provider
	// Spend prices requests for spend-denominated plans; nil counts only
	// measured cost.
	Spend *spending.Engine
	// AdviceCache keeps the glance's plan advice between calls; nil
	// replays it on every glance.
	AdviceCache *headroom.AdviceCache
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

// planStoreReader adapts *sqlite.Store to headroom.Reader.
type planStoreReader struct{ store *sqlite.Store }

// CountBySource satisfies headroom.Reader, so the capability can be
// handed one port instead of a reader plus a loose function value.
func (r planStoreReader) CountBySource(ctx context.Context, since, until time.Time) (map[string]int64, error) {
	return r.store.CountBySource(ctx, since, until)
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
	s.Tool("tokenops_plan_advice").
		Description("Plan right-sizing: replay the last days (30 by default, up to 90) of each bound plan's windows against its other tiers, the tiers with metered overflow, and the API at list price. Per option: simulated lockouts and minutes blocked, monthly cost, and the peak window share; then a recommendation with a confidence label. Claude tiers derive from an unverified Pro baseline, which the caveats say. Advice only: it never switches or binds a plan.").
		OutputSchema(planAdviceResult{}).
		Handler(func(ctx context.Context, in planAdviceInput) (*planAdviceResult, error) {
			return planAdvice(ctx, d, in)
		})
	registerResourceGlanceTool(s, d)
	return nil
}

// planAdviceResult is the capability's wire payload, shared with GET
// /api/plans/advice and `tokenops plan advise --json`.
type planAdviceResult = headroom.AdvicePayload

type planAdviceInput struct {
	Days int `json:"days,omitempty" jsonschema:"minimum=1,maximum=90,description=how far back to replay (default 30)"`
}

func planAdvice(ctx context.Context, d PlanDeps, in planAdviceInput) (*planAdviceResult, error) {
	if in.Days < 0 || in.Days > headroom.MaxAdviceDays {
		return nil, inputError(fmt.Errorf("days must be from 1 to %d", headroom.MaxAdviceDays))
	}
	res, err := headroom.Advise(ctx, d.headroomDeps(), headroom.AdviceQuery{Days: in.Days}, time.Now().UTC())
	if err != nil {
		return nil, err
	}
	return res.Payload(), nil
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
	deps := headroom.Deps{Config: d.activeConfig(), PlanFollows: actions.RecentPlanFollows,
		PlanHistory: planhistory.LoadDefault, AdviceCache: d.AdviceCache, Observed: observedlimits.Loader()}
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
