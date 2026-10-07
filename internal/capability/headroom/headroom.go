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
	// Price prices a request at list rates, for spend-denominated plans
	// whose usage is billed per token. nil counts only measured cost.
	Price plans.Pricer
	// Accounts names the account each provider's client is signed in
	// with, for the glance. nil names none.
	Accounts func() map[string]string
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
// Both restart a supervised daemon themselves, so this does not end with
// "then restart", and an agent reading the hint can bind the plan itself.
// It used to also offer TOKENOPS_PLAN_<PROVIDER> as "picked up without a
// restart"; that variable is read once, when a process loads its config,
// so set in a shell it never reached a running daemon.
const UnconfiguredHint = "no plans configured; bind one with `tokenops plan set <provider> <plan>` " +
	"(e.g. `tokenops plan set anthropic claude-max-20x`) or tokenops_configure (setting=plan); " +
	"either restarts a supervised daemon so the plan applies at once"

// StorageDisabledHint says how to get an event store.
const StorageDisabledHint = "no event store: run `tokenops init`, then restart the daemon"

// Compute answers headroom for every configured plan.
//
// now is injected so a caller can ask about a fixed moment and so tests
// do not depend on the clock.
func Compute(ctx context.Context, d Deps, now time.Time) (Result, error) {
	if d.Config == nil {
		return Result{Unconfigured: UnconfiguredHint}, nil
	}
	if d.Reader == nil {
		if len(d.Config.Plans) == 0 {
			return Result{Unconfigured: UnconfiguredHint}, nil
		}
		return Result{StorageDisabled: StorageDisabledHint}, nil
	}
	d.Reader = newMemoReader(d.Reader, now)
	bindings, inferred, err := effectiveBindings(ctx, d, now)
	if err != nil {
		return Result{}, fmt.Errorf("headroom bindings: %w", err)
	}
	if len(bindings) == 0 {
		return Result{Unconfigured: UnconfiguredHint}, nil
	}

	var out Result
	out.Reports = make([]plans.HeadroomReport, 0, len(bindings))

	for _, provider := range sortedProviders(bindings) {
		planName := bindings[provider]
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
				Price:      d.Price,
			}, now)
		if err != nil {
			return Result{}, fmt.Errorf("headroom inputs[%s]: %w", provider, err)
		}
		report, err := plans.ComputeHeadroom(planName, inputs)
		if err != nil {
			return Result{}, fmt.Errorf("headroom[%s]: %w", provider, err)
		}
		// A plan that belongs to no provider (pay-as-you-go) reports
		// under the provider it is bound to.
		if report.Provider == "" {
			report.Provider = provider
		}
		switch inferred[provider] {
		case fromSubscription:
			if report.Note == "" {
				report.Note = provider + " reports a subscription; its windows are the vendor's own. " +
					"`tokenops plan set " + provider + " <plan>` names the tier"
			}
		case fromReading:
			if report.Note == "" {
				report.Note = provider + " reports this account as billed per token; " +
					"`tokenops plan set " + provider + " <plan>` binds another plan"
			}
		case fromUsage:
			// Free models and trials cost nothing; a row of $0 says nothing.
			if report.SpendUSD <= 0 {
				continue
			}
			if report.Note == "" || report.SpendLimitUSD <= 0 {
				report.Note = "billed per token this month; no limit known — " +
					"`tokenops plan set " + provider + " pay-as-you-go --spend-limit <usd>` sets one"
			}
		}
		out.Reports = append(out.Reports, report)
	}
	if len(out.Reports) == 0 && len(out.Notes) == 0 {
		out.Unconfigured = UnconfiguredHint
	}
	return out, nil
}

// inference says why a provider was bound without the operator.
type inference int

// The zero value is a binding the operator made.
const (
	// fromReading: the vendor's own reader reported a per-token account.
	fromReading inference = iota + 1
	// fromUsage: usage billed per token was seen this month.
	fromUsage
	// fromSubscription: the vendor's own reader reported a plan's windows.
	fromSubscription
)

// effectiveBindings is the configured plans plus, for each provider with
// no plan bound (ADR 0009 §7: evidence first, the operator corrects): a
// subscription where the vendor's own reader reported one, pay-as-you-go
// where it reported a per-token account, then pay-as-you-go for any
// metered usage. inferred
// says how each added one was found.
func effectiveBindings(ctx context.Context, d Deps, now time.Time) (map[string]string, map[string]inference, error) {
	out := make(map[string]string, len(d.Config.Plans)+2)
	for p, name := range d.Config.Plans {
		out[p] = name
	}
	inferred := map[string]inference{}
	// A vendor that reports a subscription's windows is on a plan, which
	// wins over usage that merely looks metered.
	sources := []struct {
		providers func(context.Context, plans.EventReader, time.Time) ([]string, error)
		plan      string
		how       inference
	}{
		{plans.SubscriptionReadingProviders, plans.Subscription, fromSubscription},
		{plans.PerTokenReadingProviders, plans.PayAsYouGo, fromReading},
		{plans.MeteredProviders, plans.PayAsYouGo, fromUsage},
	}
	for _, src := range sources {
		providers, err := src.providers(ctx, d.Reader, now)
		if err != nil {
			return nil, nil, err
		}
		for _, p := range providers {
			if _, ok := out[p]; !ok {
				out[p], inferred[p] = src.plan, src.how
			}
		}
	}
	return out, inferred, nil
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
