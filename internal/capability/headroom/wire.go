package headroom

import (
	"go.klarlabs.de/tokenops/internal/contexts/spend/plans"
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

// Payload is g on the wire.
func (g Glance) Payload() *GlancePayload {
	return &GlancePayload{
		Insight:       g.Insight,
		SessionBudget: *g.Budgets.Payload(),
		PlanHeadroom:  g.Headroom.Payload(),
	}
}
