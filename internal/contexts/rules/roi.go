package rules

import (
	"time"

	"github.com/felixgeelhaar/tokenops/pkg/eventschema"
)

// Exposure aggregates how a single rule (or section) was exercised across
// a measurement window. The ROI engine consumes Exposures alongside the
// rule's static cost (from RuleSourceEvent) to produce ROI snapshots.
//
// Counts in this struct are observable signals — the proxy and analytics
// pipeline are the authoritative producers. Tests substitute hand-built
// values so the ROI engine can be exercised without the full event
// pipeline.
type Exposure struct {
	// SourceID identifies the rule artifact.
	SourceID string
	// SectionID, when set, narrows the exposure to one section.
	SectionID string
	// WorkflowID / AgentID propagate attribution; empty means workflow-
	// agnostic rollup.
	WorkflowID string
	AgentID    string

	// WindowStart / WindowEnd bound the measurement window.
	WindowStart time.Time
	WindowEnd   time.Time

	// Requests counts how many requests included the rule in their context
	// across the window.
	Requests int64
	// RuleContextTokens is the per-request token cost of carrying the rule
	// (typically RuleSection.TokenCount or RuleSourceEvent.TotalTokens).
	RuleContextTokens int64

	// OutputTokens is the cumulative output token count observed in the
	// window. Used as a baseline for "context reduction" heuristics.
	OutputTokens int64
	// BaselineOutputTokens is the predicted output token count had the
	// rule been absent. Difference seeds TokensSaved. When zero the
	// engine falls back to a configured assumption.
	BaselineOutputTokens int64

	// Retries observed across the window. The engine treats reductions
	// vs the configured assumed retry rate as "retries avoided".
	Retries int64

	// LatencyDeltaNS is the cumulative wall-clock delta vs baseline
	// (negative = faster with rule).
	LatencyDeltaNS int64

	// QualityScore is the average optimizer quality score observed for
	// requests in this window (0.0 to 1.0).
	QualityScore float64
	// BaselineQualityScore is the optimizer quality score the engine
	// compares against. Difference seeds QualityDelta.
	BaselineQualityScore float64
}

// ROIConfig tunes the ROI engine's heuristics. Zero values produce sane
// defaults sufficient for the initial slice; downstream tuning lives in
// the optimization-quality-evals-framework feature.
type ROIConfig struct {
	// AssumedRetryRate is the retry probability assumed when no baseline
	// retry observation is available. The engine treats observed retries
	// below this rate as "retries avoided". Default 0.10.
	AssumedRetryRate float64
	// AssumedSavingsPerRetry is the average token cost of a retry the
	// engine charges back to the rule when retries are avoided. Default
	// 800 (input + output).
	AssumedSavingsPerRetry int64
	// BaselineGrowthFactor is the assumed multiplier on OutputTokens when
	// no explicit baseline is available — i.e. how much *bigger* the
	// output would have been without the rule. Default 1.08 (rule shrinks
	// output by ~7.4%).
	BaselineGrowthFactor float64
}

// withDefaults returns c with zero fields replaced by package defaults.
func (c ROIConfig) withDefaults() ROIConfig {
	if c.AssumedRetryRate == 0 {
		c.AssumedRetryRate = 0.10
	}
	if c.AssumedSavingsPerRetry == 0 {
		c.AssumedSavingsPerRetry = 800
	}
	if c.BaselineGrowthFactor == 0 {
		c.BaselineGrowthFactor = 1.08
	}
	return c
}

// ROIEngine produces RuleAnalysisEvent snapshots from rule exposures.
type ROIEngine struct {
	cfg ROIConfig
}

// NewROIEngine returns an engine with the given configuration.
func NewROIEngine(cfg ROIConfig) *ROIEngine {
	return &ROIEngine{cfg: cfg.withDefaults()}
}

// Analyze produces one RuleAnalysisEvent per exposure.
func (e *ROIEngine) Analyze(exposures []Exposure) []*eventschema.RuleAnalysisEvent {
	out := make([]*eventschema.RuleAnalysisEvent, 0, len(exposures))
	for _, x := range exposures {
		out = append(out, e.score(x))
	}
	return out
}

func (e *ROIEngine) score(x Exposure) *eventschema.RuleAnalysisEvent {
	ev := &eventschema.RuleAnalysisEvent{
		SourceID:        x.SourceID,
		SectionID:       x.SectionID,
		WorkflowID:      x.WorkflowID,
		AgentID:         x.AgentID,
		WindowStart:     x.WindowStart,
		WindowEnd:       x.WindowEnd,
		Exposures:       x.Requests,
		ContextTokens:   x.RuleContextTokens * max(x.Requests, int64(0)),
		LatencyImpactNS: x.LatencyDeltaNS,
		QualityDelta:    x.QualityScore - x.BaselineQualityScore,
	}

	// Tokens saved: baseline-output minus observed-output, plus retries
	// avoided x per-retry savings.
	baseline := x.BaselineOutputTokens
	if baseline == 0 {
		baseline = int64(float64(x.OutputTokens) * e.cfg.BaselineGrowthFactor)
	}
	tokenSaved := max(baseline-x.OutputTokens, int64(0))

	assumedRetries := int64(float64(x.Requests) * e.cfg.AssumedRetryRate)
	retriesAvoided := max(assumedRetries-x.Retries, int64(0))
	tokenSaved += retriesAvoided * e.cfg.AssumedSavingsPerRetry
	ev.TokensSaved = tokenSaved
	ev.RetriesAvoided = retriesAvoided

	if baseline > 0 {
		ev.ContextReduction = float64(baseline-x.OutputTokens) / float64(baseline)
	}

	// ROIScore: net token economics, normalized by rule context cost so
	// the score is comparable across rules of different sizes.
	cost := ev.ContextTokens
	if cost <= 0 {
		ev.ROIScore = 0
		return ev
	}
	ev.ROIScore = float64(ev.TokensSaved-cost) / float64(cost)
	return ev
}
