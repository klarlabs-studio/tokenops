package headroom

import (
	"time"

	bcap "go.klarlabs.de/tokenops/internal/capability/boundaries"
	"go.klarlabs.de/tokenops/internal/capability/spending"
	"go.klarlabs.de/tokenops/internal/contexts/coaching/waste"
	"go.klarlabs.de/tokenops/internal/contexts/spend/plans"
	"go.klarlabs.de/tokenops/internal/infra/boundaryledger"
	"go.klarlabs.de/tokenops/internal/presentation"
)

// The payloads below are the one wire contract for these answers: the MCP
// tools' structured output and the daemon API's JSON (ADR 0010 §4). An
// unconfigured or storage-less answer is {error, hint}, the same on every
// surface.

// Error codes in a payload's error field.
const (
	ErrPlansUnconfigured = "plans_unconfigured"
	ErrStorageDisabled   = "storage_disabled"
)

// HeadroomPayload is plan headroom on the wire.
type HeadroomPayload struct {
	Reports []plans.HeadroomReport `json:"reports,omitempty"`
	// Notes names plans that could not be reported on. An unknown plan
	// name used to be skipped silently, so an operator's typo produced a
	// plan that reported nothing and said nothing, forever.
	Notes []string `json:"notes,omitempty"`
	Error string   `json:"error,omitempty"`
	Hint  string   `json:"hint,omitempty"`
}

// BudgetPayload is the session budget on the wire.
type BudgetPayload struct {
	Budgets []plans.SessionBudget `json:"budgets"`
	Notes   []string              `json:"notes,omitempty"`
	Error   string                `json:"error,omitempty"`
	Hint    string                `json:"hint,omitempty"`
}

// GlancePayload is the glance on the wire.
type GlancePayload struct {
	Insight       presentation.ResourceInsight `json:"insight"`
	SessionBudget BudgetPayload                `json:"session_budget"`
	PlanHeadroom  *HeadroomPayload             `json:"plan_headroom"`
	// Accounts is the account each provider's client is signed in with,
	// for the operator's own view. It never leaves the machine: the
	// telemetry export reads figures only.
	Accounts map[string]string `json:"accounts,omitempty"`
	// PlanUse is each provider's plan utilization (mean peak of its
	// longest window kind) and coverage (share of tokens a plan covered)
	// over the last week. Figures only, never prompts.
	PlanUse *plans.PlanUseReport `json:"plan_use,omitempty"`
	// PlanAdvice is each bound plan replayed against its other tiers over
	// the last 30 days: lockouts, cost and peak window share per option,
	// and a recommendation. Advice only; nothing is switched.
	PlanAdvice []plans.Advice `json:"plan_advice,omitempty"`
	// EarlyResets are plan windows that reset early within the last day:
	// the vendor reset them before the time it had given, and nothing the
	// operator did explains it.
	EarlyResets []plans.EarlyReset `json:"early_resets,omitempty"`
	// Boundaries are the operator's boundaries that hold now, as advice
	// for an agent whose harness cannot be refused (ADR 0014). The caller
	// sets them; Payload leaves them empty.
	Boundaries []bcap.Advice `json:"boundaries,omitempty"`
	// SpendBudgets are the operator's spend budgets, one entry per
	// window, as the daemon last measured them: share used, mode, and for
	// a hard window the boundary that enforces it. The caller sets them;
	// Payload leaves them empty.
	SpendBudgets []boundaryledger.BudgetReading `json:"spend_budgets,omitempty"`
	// ComputedAt is when the glance was computed. The daemon answers from
	// its last computation (and, after a start, from the one it saved),
	// so a reader shows this, not when it asked. The caller sets it;
	// Payload leaves it empty.
	ComputedAt time.Time `json:"computed_at,omitzero"`
	// Routing tells the agent, while a plan is running short, to pass a
	// cheaper model on subagent calls for work that fits, and why. Absent
	// under no pressure.
	Routing []Routing `json:"routing,omitempty"`
	// PromptCache is the time until the current Claude Code session's
	// prompt cache goes cold: warm, expiring with under two minutes left,
	// or cold, after which the next prompt writes the whole context to the
	// cache again. Absent with no session current.
	PromptCache *waste.CacheCountdown `json:"prompt_cache,omitempty"`
	// ProviderCosts is each plan's provider's usage today and over the
	// last 30 days, as the cards show it; a provider whose figures could
	// not be read is absent. The caller sets it; Payload leaves it empty.
	ProviderCosts map[string]spending.ProviderCost `json:"provider_costs,omitempty"`
	// ProviderCostsLate is set when the costs were not all read in time.
	ProviderCostsLate bool `json:"provider_costs_late,omitempty"`
}

// Payload is r on the wire.
func (r Result) Payload() *HeadroomPayload {
	switch {
	case r.Unconfigured != "":
		return &HeadroomPayload{Error: ErrPlansUnconfigured, Hint: r.Unconfigured}
	case r.StorageDisabled != "":
		return &HeadroomPayload{Error: ErrStorageDisabled, Hint: r.StorageDisabled}
	}
	return &HeadroomPayload{Reports: r.Reports, Notes: r.Notes}
}

// Payload is r on the wire.
func (r BudgetResult) Payload() *BudgetPayload {
	switch {
	case r.Unconfigured != "":
		return &BudgetPayload{Error: ErrPlansUnconfigured, Hint: r.Unconfigured}
	case r.StorageDisabled != "":
		return &BudgetPayload{Error: ErrStorageDisabled, Hint: r.StorageDisabled}
	}
	return &BudgetPayload{Budgets: r.Budgets, Notes: r.Notes}
}

// Glance is the glance p carries, as Payload made it: what a client that
// asked the daemon renders, as it renders one it computed.
func (p *GlancePayload) Glance() Glance {
	g := Glance{Insight: p.Insight, Budgets: p.SessionBudget.result(), Accounts: p.Accounts, PlanUse: p.PlanUse,
		PlanAdvice: p.PlanAdvice, EarlyResets: p.EarlyResets, Routing: p.Routing, PromptCache: p.PromptCache}
	if p.PlanHeadroom != nil {
		g.Headroom = p.PlanHeadroom.result()
	}
	return g
}

// result is the Result p carries.
func (p HeadroomPayload) result() Result {
	switch p.Error {
	case ErrPlansUnconfigured:
		return Result{Unconfigured: p.Hint}
	case ErrStorageDisabled:
		return Result{StorageDisabled: p.Hint}
	}
	return Result{Reports: p.Reports, Notes: p.Notes}
}

// result is the BudgetResult p carries.
func (p BudgetPayload) result() BudgetResult {
	switch p.Error {
	case ErrPlansUnconfigured:
		return BudgetResult{Unconfigured: p.Hint}
	case ErrStorageDisabled:
		return BudgetResult{StorageDisabled: p.Hint}
	}
	return BudgetResult{Budgets: p.Budgets, Notes: p.Notes}
}

// Payload is g on the wire.
func (g Glance) Payload() *GlancePayload {
	return &GlancePayload{
		Insight:       g.Insight,
		SessionBudget: *g.Budgets.Payload(),
		PlanHeadroom:  g.Headroom.Payload(),
		Accounts:      g.Accounts,
		PlanUse:       g.PlanUse,
		PlanAdvice:    g.PlanAdvice,
		EarlyResets:   g.EarlyResets,
		Routing:       g.Routing,
		PromptCache:   g.PromptCache,
	}
}

// VendorWindow is one of the vendor's own rate-limit windows as a session
// budget carries it.
type VendorWindow = plans.VendorWindow

// PromptCacheTurns is when g's prompt cache countdown next changes state
// with no request: to expiring, then to cold. Empty with no session.
func (g Glance) PromptCacheTurns() []time.Time {
	if g.PromptCache == nil {
		return nil
	}
	e := g.PromptCache.ExpiresAt
	return []time.Time{e.Add(-waste.CacheExpiringWithin), e}
}
