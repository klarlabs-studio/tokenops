// Package headroom is the plan-headroom capability: one implementation
// of "how much of each configured plan have I used", which the CLI and
// the MCP server both call instead of each writing their own.
//
// It is the first migration of ADR 0004 Phase 4, and headroom was chosen
// because its divergence had already produced a bug. The MCP tool sorted
// the configured providers before iterating; the CLI ranged the map
// directly. Go randomises map iteration, so on the CLI side "the first
// plan" — the one printed first, the one an operator skims — was a
// different plan from run to run. The fix was written once, in one of
// the two places that needed it, because nothing connected them.
//
// internal/cli/parity_test.go guards that every CLI command has a
// matching MCP tool and vice versa. It passed throughout. Name parity is
// not capability parity, and this layer is what makes the second kind
// checkable.
//
// # What belongs here and what does not
//
// The capability owns orchestration: which plans, in what order, with
// which limits, and what to answer when there is nothing to report. The
// arithmetic stays in the plans domain. Rendering — a table, JSON, a
// tool result — stays in the adapter, because that genuinely differs.
package headroom

import (
	"context"
	"fmt"
	"sort"
	"time"

	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/contexts/spend/plans"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// Reader is everything this capability needs from persistence.
//
// Declared here rather than taken as *sqlite.Store so the capability
// stays testable without a database, and so an adapter can supply any
// store that can answer these two questions. Both adapters previously
// wrote their own one-method wrapper around the same store; they now
// share this.
type Reader interface {
	ReadEvents(ctx context.Context, t eventschema.EventType, since time.Time) ([]*eventschema.Envelope, error)
	CountBySource(ctx context.Context, since, until time.Time) (map[string]int64, error)
}

// Deps is what a caller supplies.
type Deps struct {
	// Config holds the plan bindings and per-provider limits. nil is
	// treated as no plans configured.
	Config *config.Config
	// Reader is the event store. nil means storage is disabled, which is
	// a different answer from having no plans.
	Reader Reader
}

// Result is the capability's answer.
//
// Unconfigured and StorageDisabled are separate fields on purpose. Both
// adapters had their own wording for these two states, and conflating
// them sends an operator to fix the wrong thing — one is "bind a plan",
// the other is "run tokenops init".
type Result struct {
	// Reports is one entry per configured plan, in provider order.
	Reports []plans.HeadroomReport
	// Notes names plans that could not be reported on, one per cause.
	// An unknown plan is named rather than dropped: skipping it silently
	// is how an operator's typo becomes a plan that reports nothing
	// forever.
	Notes []string
	// Unconfigured, when set, explains that no plan is bound.
	Unconfigured string
	// StorageDisabled, when set, explains that there is no event store.
	StorageDisabled string
}

// Answered reports whether there is anything to render.
func (r Result) Answered() bool { return len(r.Reports) > 0 }

// UnconfiguredHint names both ways to bind a plan.
//
// Config hot-reloads, so this deliberately does not end with "then
// restart" — that was a step which changed nothing, and an agent reading
// the hint can bind the plan itself.
const UnconfiguredHint = "no plans configured; bind one with `tokenops plan set <provider> <plan>` " +
	"(e.g. `tokenops plan set anthropic claude-max-20x`) or tokenops_plan_set, " +
	"or set TOKENOPS_PLAN_<PROVIDER>; the change is picked up without a restart"

// StorageDisabledHint says how to get an event store.
const StorageDisabledHint = "no event store: run `tokenops init`, then restart the daemon"

// Compute answers headroom for every configured plan.
//
// now is injected so a caller can ask about a fixed moment and so tests
// do not depend on the clock.
func Compute(ctx context.Context, d Deps, now time.Time) (Result, error) {
	if d.Config == nil || len(d.Config.Plans) == 0 {
		return Result{Unconfigured: UnconfiguredHint}, nil
	}
	if d.Reader == nil {
		return Result{StorageDisabled: StorageDisabledHint}, nil
	}

	var out Result
	out.Reports = make([]plans.HeadroomReport, 0, len(d.Config.Plans))

	for _, provider := range sortedProviders(d.Config.Plans) {
		planName := d.Config.Plans[provider]
		if _, known := plans.Lookup(planName); !known {
			out.Notes = append(out.Notes,
				fmt.Sprintf("%s: %q is not a plan TokenOps knows; "+
					"run `tokenops plan list` to see the catalog", provider, planName))
			continue
		}

		lim := d.Config.PlanLimits[provider]
		inputs, err := plans.AssembleHeadroomInputs(ctx, d.Reader, d.Reader.CountBySource,
			provider, planName,
			plans.SpendLimit{
				LimitUSD:   lim.SpendLimitUSD,
				Window:     lim.Window,
				RateFactor: lim.RateFactor,
			}, now)
		if err != nil {
			return Result{}, fmt.Errorf("headroom inputs[%s]: %w", provider, err)
		}
		report, err := plans.ComputeHeadroom(planName, inputs)
		if err != nil {
			return Result{}, fmt.Errorf("headroom[%s]: %w", provider, err)
		}
		out.Reports = append(out.Reports, report)
	}
	return out, nil
}

// sortedProviders returns the configured providers in a stable order.
//
// Stable means sorted rather than merely repeatable: a caller that
// renders "the first plan" should get the same one on every machine and
// every run, which is exactly what ranging the map did not give.
func sortedProviders(bindings map[string]string) []string {
	out := make([]string, 0, len(bindings))
	for p := range bindings {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}
