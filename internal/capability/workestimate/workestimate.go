// Package workestimate is the use case behind tokenops_prepare_work's
// window-share estimate and tokenops_review_work's estimate-versus-actual.
//
// At prepare it classifies the task, reads the plan window, estimates the
// task's share of it from earlier measured tasks of the same class and
// scope, and records where the window stood. At review it reads the
// window again, measures the share the task took, scores the estimate,
// and records the result, which becomes history for the next estimate.
//
// The records are derived figures only: the task class, an opaque scope
// label, percentages and times. The instruction is classified in memory
// and never stored.
package workestimate

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/contexts/optimization/modeltier"
	"go.klarlabs.de/tokenops/internal/contexts/optimization/taskclass"
	"go.klarlabs.de/tokenops/internal/contexts/spend/plans"
	"go.klarlabs.de/tokenops/internal/contexts/spend/spend"
	"go.klarlabs.de/tokenops/internal/contexts/work/windowshare"
	"go.klarlabs.de/tokenops/internal/storage/sqlite"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// Domain event kinds this package records.
const (
	KindEstimate = "work.window_estimate"
	KindActual   = "work.window_actual"
)

// StatusUnavailable means no windowed plan could be read for the task.
const StatusUnavailable = "unavailable"

const (
	source = "work_estimate"
	// historyLookback bounds the measured tasks an estimate draws on, so
	// a change in how the operator works ages out.
	historyLookback = 90 * 24 * time.Hour
	// prepareLookback bounds how long after prepare a review can still
	// find the task's start.
	prepareLookback = 30 * 24 * time.Hour
	queryLimit      = 100_000
)

// Store is the canonical local event store.
type Store interface {
	Append(ctx context.Context, env *eventschema.Envelope) error
	Query(ctx context.Context, f sqlite.Filter) ([]*eventschema.Envelope, error)
	ReadEvents(ctx context.Context, t eventschema.EventType, since time.Time) ([]*eventschema.Envelope, error)
}

// Deps wires Prepare.
type Deps struct {
	Config *config.Config
	Store  Store
	// MinSamples overrides windowshare.DefaultMinSamples.
	MinSamples int
}

// PrepareRequest is one task about to start.
type PrepareRequest struct {
	WorkflowID  string
	Instruction string
	// Scope is where the work happens, e.g. a repository name.
	Scope    string
	Provider string
	// Model is what the task would run on; SwitchTo is the model routing
	// advice proposed, when it proposed one.
	Model    string
	SwitchTo string
}

// Preparation is the estimate an agent reads before it starts.
type Preparation struct {
	// Status is estimated, insufficient_history or unavailable.
	Status      string                     `json:"status"`
	TaskClass   string                     `json:"task_class"`
	Scope       string                     `json:"scope,omitempty"`
	Provider    string                     `json:"provider,omitempty"`
	Window      string                     `json:"window,omitempty"`
	WindowKnown bool                       `json:"window_known"`
	UsedPct     float64                    `json:"used_pct"`
	LeftPct     float64                    `json:"left_pct"`
	ResetsIn    string                     `json:"resets_in,omitempty"`
	Estimate    windowshare.EstimateResult `json:"estimate"`
	// Spread is the p50 and p90 of past runs of the same kind with their
	// count, or why there are too few to read one. Nil only when there
	// was no history to read.
	Spread     *windowshare.Spread `json:"spread,omitempty"`
	Suggestion windowshare.Advice  `json:"suggestion"`
	Summary    string              `json:"summary"`
	Caveat     string              `json:"caveat,omitempty"`
	// Recorded says the window's state was stored, so review can measure
	// the task.
	Recorded bool `json:"recorded"`
}

// estimateRecord is the KindEstimate body.
type estimateRecord struct {
	WorkflowID    string                     `json:"workflow_id"`
	TaskClass     string                     `json:"task_class"`
	Scope         string                     `json:"scope,omitempty"`
	Provider      string                     `json:"provider"`
	Plan          string                     `json:"plan"`
	WindowSeconds int64                      `json:"window_seconds"`
	StartKnown    bool                       `json:"start_known"`
	StartUsedPct  float64                    `json:"start_used_pct"`
	StartAt       time.Time                  `json:"start_at"`
	ResetsAt      time.Time                  `json:"resets_at,omitzero"`
	Estimate      windowshare.EstimateResult `json:"estimate"`
}

// actualRecord is the KindActual body.
type actualRecord struct {
	WorkflowID    string                     `json:"workflow_id"`
	TaskClass     string                     `json:"task_class"`
	Scope         string                     `json:"scope,omitempty"`
	Provider      string                     `json:"provider"`
	WindowSeconds int64                      `json:"window_seconds"`
	Estimate      windowshare.EstimateResult `json:"estimate"`
	Actual        windowshare.Actual         `json:"actual"`
	Score         windowshare.ScoreResult    `json:"score"`
}

// Prepare estimates the task's share of the plan window and records
// where the window stands, so Review can measure what the task took.
func Prepare(ctx context.Context, d Deps, req PrepareRequest, now time.Time) (*Preparation, error) {
	kind := taskclass.ClassifyKind(req.Instruction)
	scope := strings.TrimSpace(req.Scope)
	out := &Preparation{TaskClass: string(kind), Scope: scope, Status: StatusUnavailable}

	provider, planName, plan, why := resolvePlan(d.Config, req.Provider)
	if why != "" {
		out.Summary = "no window estimate: " + why
		return out, nil
	}
	out.Provider, out.Window = provider, windowshare.WindowLabel(plan.RateLimitWindow)
	if d.Store == nil {
		out.Summary = "no window estimate: storage is disabled, so there is no history to estimate from"
		return out, nil
	}

	reading := readWindow(ctx, d.Store, eventschema.Provider(provider), plan, now)
	hist, err := history(ctx, d.Store, now)
	if err != nil {
		return nil, err
	}
	key := windowshare.Key{Kind: string(kind), Scope: scope, Provider: provider, Window: plan.RateLimitWindow}
	est := windowshare.Estimate(hist, key, d.MinSamples)
	spread := windowshare.SpreadOf(hist, key, est)
	advice := windowshare.Advise(est, reading)
	if advice.Action == windowshare.ActionLighterTier || advice.Action == windowshare.ActionDefer {
		advice.SuggestedTier = lighterTier(provider, req.Model)
		advice.SuggestedModel = strings.TrimSpace(req.SwitchTo)
	}

	out.Status = string(est.Status)
	out.WindowKnown, out.Estimate, out.Spread, out.Suggestion = reading.Known, est, &spread, advice
	if reading.Known {
		out.UsedPct, out.LeftPct = reading.UsedPct, reading.LeftPct()
		if in := reading.ResetsIn(); in > 0 {
			out.ResetsIn = windowshare.Duration(in)
		}
	}
	out.Summary = windowshare.SummaryWithSpread(est, spread, reading, plan.RateLimitWindow)
	out.Caveat = windowshare.Caveat

	rec := estimateRecord{
		WorkflowID: req.WorkflowID, TaskClass: string(kind), Scope: scope,
		Provider: provider, Plan: planName, WindowSeconds: int64(plan.RateLimitWindow.Seconds()),
		StartKnown: reading.Known, StartUsedPct: reading.UsedPct, StartAt: now, ResetsAt: reading.ResetsAt,
		Estimate: est,
	}
	if err := appendRecord(ctx, d.Store, KindEstimate, rec, now); err != nil {
		return nil, err
	}
	out.Recorded = true
	return out, nil
}

// ReviewResult is the estimate set against what the task took.
type ReviewResult struct {
	TaskClass string                     `json:"task_class"`
	Scope     string                     `json:"scope,omitempty"`
	Provider  string                     `json:"provider"`
	Window    string                     `json:"window"`
	Estimate  windowshare.EstimateResult `json:"estimate"`
	Actual    windowshare.Actual         `json:"actual"`
	Score     windowshare.ScoreResult    `json:"score"`
	Summary   string                     `json:"summary"`
	Caveat    string                     `json:"caveat"`
	Recorded  bool                       `json:"recorded"`
}

// Review measures the share of the window workflowID took since it was
// prepared, scores the estimate, and records both. It returns nil when
// the workflow was never prepared, since there is no start to measure
// from.
func Review(ctx context.Context, store Store, workflowID string, now time.Time) (*ReviewResult, error) {
	if store == nil || strings.TrimSpace(workflowID) == "" {
		return nil, nil
	}
	start, err := findEstimate(ctx, store, workflowID, now)
	if err != nil || start == nil {
		return nil, err
	}
	window := time.Duration(start.WindowSeconds) * time.Second
	out := &ReviewResult{
		TaskClass: start.TaskClass, Scope: start.Scope, Provider: start.Provider,
		Window: windowshare.WindowLabel(window), Estimate: start.Estimate, Caveat: windowshare.Caveat,
	}
	end := windowshare.Reading{At: now}
	if plan, ok := plans.Lookup(start.Plan); ok && plan.RateLimitWindow == window {
		end = readWindow(ctx, store, eventschema.Provider(start.Provider), plan, now)
	}
	begin := windowshare.Reading{Known: start.StartKnown, UsedPct: start.StartUsedPct, At: start.StartAt, ResetsAt: start.ResetsAt}
	out.Actual = windowshare.Measure(begin, end)
	out.Score = windowshare.Score(start.Estimate, out.Actual)
	out.Summary = reviewSummary(out)

	rec := actualRecord{
		WorkflowID: workflowID, TaskClass: start.TaskClass, Scope: start.Scope, Provider: start.Provider,
		WindowSeconds: start.WindowSeconds, Estimate: start.Estimate, Actual: out.Actual, Score: out.Score,
	}
	if err := appendRecord(ctx, store, KindActual, rec, now); err != nil {
		return nil, err
	}
	out.Recorded = true
	return out, nil
}

func reviewSummary(r *ReviewResult) string {
	if !r.Actual.Measured {
		return "share of the " + r.Window + " window not measured: " + r.Actual.Reason
	}
	s := fmt.Sprintf("took %s%% of the %s window", trimPct(r.Actual.SharePct), r.Window)
	if !r.Estimate.Estimated() {
		return s + "; no estimate was offered, and this task now counts toward one"
	}
	return s + fmt.Sprintf("; estimate was %s–%s%% (%s)", trimPct(r.Estimate.LowPct), trimPct(r.Estimate.HighPct), r.Score.Direction)
}

func trimPct(v float64) string {
	s := fmt.Sprintf("%.1f", v)
	return strings.TrimSuffix(s, ".0")
}

// resolvePlan finds the windowed plan the task runs against.
func resolvePlan(cfg *config.Config, named string) (provider, planName string, plan plans.Plan, why string) {
	if cfg == nil {
		return "", "", plans.Plan{}, "no configuration loaded"
	}
	provider = strings.TrimSpace(named)
	if provider == "" {
		if len(cfg.Plans) != 1 {
			return "", "", plans.Plan{}, "pass provider; more than one plan is configured, or none"
		}
		for p := range cfg.Plans {
			provider = p
		}
	}
	planName = cfg.Plans[provider]
	p, ok := plans.Lookup(planName)
	if !ok {
		return provider, "", plans.Plan{}, "no plan is bound for " + provider
	}
	if p.RateLimitWindow <= 0 {
		return provider, planName, plans.Plan{}, planName + " has no rate-limit window"
	}
	return provider, planName, p, ""
}

// readWindow reads the plan window the way routing reads its pressure:
// the vendor's meter when there is a recent reading, else the plan's
// message cap against the messages counted in the window.
func readWindow(ctx context.Context, r plans.EventReader, provider eventschema.Provider, plan plans.Plan, now time.Time) windowshare.Reading {
	out := windowshare.Reading{At: now}
	if a := plans.LatestAuthoritativeWindow(ctx, r, provider, plan, now); a != nil {
		out.Known, out.UsedPct = true, a.UsedPct
		if a.ResetsIn > 0 {
			out.ResetsAt = now.Add(a.ResetsIn)
		}
		return out
	}
	if plan.MessagesPerWindow <= 0 {
		return out
	}
	win, err := plans.ConsumptionInWindow(ctx, r, string(provider), now, plan.RateLimitWindow)
	if err != nil {
		return out
	}
	out.Known = true
	out.UsedPct = float64(win.MessagesInWindow) / float64(plan.MessagesPerWindow) * 100
	if !win.FirstActivityAt.IsZero() {
		out.ResetsAt = win.FirstActivityAt.Add(plan.RateLimitWindow)
	}
	return out
}

// lighterTier names the capability tier below the model's, or "" when
// the model cannot be placed or is already on the lightest tier.
func lighterTier(provider, model string) string {
	if strings.TrimSpace(model) == "" {
		return ""
	}
	tier := modeltier.New(spend.DefaultTable(), nil).Resolve(eventschema.Provider(provider), model).Tier
	switch tier {
	case modeltier.TierDeep:
		return string(modeltier.TierDefault)
	case modeltier.TierDefault:
		return string(modeltier.TierBalanced)
	case modeltier.TierBalanced:
		return string(modeltier.TierLookup)
	}
	return ""
}

func appendRecord(ctx context.Context, store Store, kind string, body any, now time.Time) error {
	env, err := eventschema.NewDomainEnvelope(kind, body, now, source, eventschema.Association{})
	if err != nil {
		return err
	}
	return store.Append(ctx, env)
}

// domainRecords decodes every recorded body of kind since the cutoff,
// oldest first.
func domainRecords[T any](ctx context.Context, store Store, kind string, since time.Time) ([]T, []time.Time, error) {
	envs, err := store.Query(ctx, sqlite.Filter{Type: eventschema.EventTypeDomain, Since: since, Limit: queryLimit})
	if err != nil {
		return nil, nil, err
	}
	var (
		out []T
		at  []time.Time
	)
	for _, env := range envs {
		d, ok := env.Payload.(*eventschema.DomainEvent)
		if !ok || d.Kind != kind {
			continue
		}
		var rec T
		if json.Unmarshal(d.Data, &rec) != nil {
			continue
		}
		out = append(out, rec)
		at = append(at, env.Timestamp)
	}
	return out, at, nil
}

// findEstimate is the first preparation of workflowID: the start of the
// task, however often it was prepared again.
func findEstimate(ctx context.Context, store Store, workflowID string, now time.Time) (*estimateRecord, error) {
	recs, _, err := domainRecords[estimateRecord](ctx, store, KindEstimate, now.Add(-prepareLookback))
	if err != nil {
		return nil, err
	}
	for i := range recs {
		if recs[i].WorkflowID == workflowID {
			return &recs[i], nil
		}
	}
	return nil, nil
}

// history is the measured share of every reviewed task, the latest
// measurement per workflow, so a task reviewed twice counts once.
func history(ctx context.Context, store Store, now time.Time) ([]windowshare.Sample, error) {
	recs, at, err := domainRecords[actualRecord](ctx, store, KindActual, now.Add(-historyLookback))
	if err != nil {
		return nil, err
	}
	latest := map[string]int{}
	var order []string
	for i, r := range recs {
		if !r.Actual.Measured {
			continue
		}
		if _, seen := latest[r.WorkflowID]; !seen {
			order = append(order, r.WorkflowID)
		}
		latest[r.WorkflowID] = i
	}
	out := make([]windowshare.Sample, 0, len(order))
	for _, id := range order {
		r := recs[latest[id]]
		out = append(out, windowshare.Sample{
			Key: windowshare.Key{
				Kind: r.TaskClass, Scope: r.Scope, Provider: r.Provider,
				Window: time.Duration(r.WindowSeconds) * time.Second,
			},
			SharePct: r.Actual.SharePct, At: at[latest[id]],
		})
	}
	return out, nil
}
