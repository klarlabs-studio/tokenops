package headroom

import (
	"context"
	"fmt"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/spend/plans"
	"go.klarlabs.de/tokenops/internal/presentation"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// recentWindow is how far back the session budget measures the current
// pace.
const recentWindow = 30 * time.Minute

// BudgetResult is the session budget for every bound plan with a rolling
// window.
type BudgetResult struct {
	Budgets []plans.SessionBudget
	// Notes explains each bound plan that has no session budget: an
	// unknown plan, or one with no rate-limit window (spend-billed
	// Enterprise). Skipping it silently read the same as "nothing bound".
	Notes           []string
	Unconfigured    string
	StorageDisabled string
}

// SessionBudgets answers "how much of my window is left, and at this pace
// when will it run out" for every bound plan with a rolling window. The
// vendor's own windows win over the message-count estimate wherever a
// reader recorded them.
func SessionBudgets(ctx context.Context, d Deps, now time.Time) (BudgetResult, error) {
	if d.Config == nil || len(d.Config.Plans) == 0 {
		return BudgetResult{Unconfigured: UnconfiguredHint}, nil
	}
	if d.Reader == nil {
		return BudgetResult{StorageDisabled: StorageDisabledHint}, nil
	}
	out := BudgetResult{Budgets: make([]plans.SessionBudget, 0, len(d.Config.Plans))}
	for _, provider := range sortedProviders(d.Config.Plans) {
		planName := d.Config.Plans[provider]
		p, ok := plans.Lookup(planName)
		if !ok {
			out.Notes = append(out.Notes, fmt.Sprintf("%s: %s is not a known plan", provider, planName))
			continue
		}
		if p.RateLimitWindow <= 0 {
			out.Notes = append(out.Notes, windowlessPlanNote(provider, planName, p))
			continue
		}
		budget, err := sessionBudget(ctx, d.Reader, provider, planName, p, now)
		if err != nil {
			return BudgetResult{}, err
		}
		out.Budgets = append(out.Budgets, budget)
	}
	return out, nil
}

func sessionBudget(ctx context.Context, r Reader, provider, planName string, p plans.Plan, now time.Time) (plans.SessionBudget, error) {
	window, err := plans.ConsumptionInWindow(ctx, r, provider, now, p.RateLimitWindow)
	if err != nil {
		return plans.SessionBudget{}, fmt.Errorf("window[%s]: %w", provider, err)
	}
	recent, err := plans.ConsumptionInWindow(ctx, r, provider, now, recentWindow)
	if err != nil {
		return plans.SessionBudget{}, fmt.Errorf("recent[%s]: %w", provider, err)
	}
	counts, err := r.CountBySource(ctx, now.Add(-p.RateLimitWindow), now)
	if err != nil {
		return plans.SessionBudget{}, fmt.Errorf("signal[%s]: %w", provider, err)
	}
	budget, err := plans.ComputeSessionBudget(planName, plans.SessionBudgetInputs{
		WindowMessages:  window.MessagesInWindow,
		WindowStartedAt: window.FirstActivityAt,
		RecentMessages:  recent.MessagesInWindow,
		RecentWindow:    recentWindow,
		Signal:          plans.SignalFromCounts(counts, provider),
		Now:             now,
		Authoritative:   plans.LatestAuthoritativeWindow(ctx, r, eventschema.Provider(provider), p, now),
		VendorWindows:   plans.VendorWindows(ctx, r, eventschema.Provider(provider), now),
	})
	if err != nil {
		return plans.SessionBudget{}, fmt.Errorf("budget[%s]: %w", provider, err)
	}
	return budget, nil
}

// windowlessPlanNote explains a plan that has no session budget to report.
func windowlessPlanNote(provider, planName string, p plans.Plan) string {
	if p.SpendDenominated {
		return fmt.Sprintf("%s: %s is billed by spend and has no rate-limit window, so there is no session budget; "+
			"call tokenops_plan_headroom for spend against the limit", provider, planName)
	}
	return fmt.Sprintf("%s: %s has no rolling rate-limit window, so there is no session budget; "+
		"call tokenops_plan_headroom for month-to-date consumption", provider, planName)
}

// Glance is one compact view of resource pressure: the session budgets,
// plan headroom, and an insight composed from both. It never changes a
// model or applies an action.
type Glance struct {
	Insight  presentation.ResourceInsight
	Budgets  BudgetResult
	Headroom Result
}

// ComputeGlance composes the session budgets and plan headroom.
func ComputeGlance(ctx context.Context, d Deps, now time.Time) (Glance, error) {
	budgets, err := SessionBudgets(ctx, d, now)
	if err != nil {
		return Glance{}, err
	}
	head, err := Compute(ctx, d, now)
	if err != nil {
		return Glance{}, err
	}
	return Glance{Insight: Insight(budgets, head), Budgets: budgets, Headroom: head}, nil
}

// Insight ranks the signals in budgets and headroom into one orientation.
func Insight(budgets BudgetResult, head Result) presentation.ResourceInsight {
	var signals []presentation.ResourceSignal
	if budgets.Unconfigured == "" && budgets.StorageDisabled == "" {
		for _, b := range budgets.Budgets {
			signals = append(signals, presentation.ResourceSignal{
				Provider:           b.Provider,
				Display:            b.Display,
				Basis:              "session_budget",
				RecommendedAction:  b.RecommendedAction,
				WindowPct:          b.WindowPct,
				Confidence:         b.Confidence,
				SignalQualityLevel: b.SignalQuality.Level,
				Caveat:             b.SignalQuality.Caveat,
			})
		}
	}
	if head.Unconfigured == "" && head.StorageDisabled == "" {
		for _, report := range head.Reports {
			signals = append(signals, presentation.ResourceSignal{
				Provider:           report.Provider,
				Display:            report.Display,
				Basis:              "plan_headroom",
				OverageRisk:        report.OverageRisk,
				SignalQualityLevel: report.SignalQuality.Level,
				Caveat:             report.SignalQuality.Caveat,
			})
		}
	}
	return presentation.ForResources(signals)
}
