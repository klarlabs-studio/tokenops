// Package qualitygate refuses optimisations whose predicted quality
// preservation falls below a configurable threshold. It plugs into the
// optimizer pipeline two ways:
//
//   - NewDecider returns a optimizer.Decider for interactive mode that
//     rejects recommendations whose QualityScore < threshold. Higher-
//     scoring recs flow through to the user-supplied decider (or are
//     accepted if no inner decider is configured).
//
//   - Wrap returns an optimizer.Optimizer that runs an inner optimizer
//     and post-filters its recommendations: low-quality recs keep their
//     metadata for telemetry but lose their ApplyBody, so the pipeline
//     records them as observed but never applies them. The filter
//     rewrites Reason to "quality_below_threshold:<original>" so
//     dashboards can attribute the gate.
//
// The gate is intentionally lightweight: scoring is the upstream
// optimizer's responsibility (compression, trim, router each return a
// QualityScore in [0,1]); this package only enforces the threshold.
package qualitygate

import (
	"context"
	"fmt"

	"github.com/felixgeelhaar/tokenops/internal/contexts/optimization/optimizer"
	"github.com/felixgeelhaar/tokenops/pkg/eventschema"
)

// DefaultThreshold is the quality floor used when none is supplied.
// 0.85 is the empirical break-point in published compression studies
// where downstream task quality falls off rapidly.
const DefaultThreshold = 0.85

// reasonPrefix is prepended to the Reason field of recommendations the
// gate disarms, so downstream telemetry can attribute the gate.
const reasonPrefix = "quality_below_threshold"

// NewDecider returns a optimizer.Decider that accepts a recommendation
// only when QualityScore >= threshold. inner, when non-nil, is consulted
// after the gate passes; this lets callers compose the gate with a
// human-in-the-loop UI prompt.
func NewDecider(threshold float64, inner optimizer.Decider) optimizer.Decider {
	if threshold <= 0 {
		threshold = DefaultThreshold
	}
	return func(ctx context.Context, rec optimizer.Recommendation) (bool, error) {
		if rec.QualityScore < threshold {
			return false, nil
		}
		if inner == nil {
			return true, nil
		}
		return inner(ctx, rec)
	}
}

// Wrap returns an Optimizer that runs inner and post-filters its
// recommendations. Low-quality recs lose their ApplyBody and have their
// Reason annotated; high-quality recs pass through unchanged. Wrap is
// the ergonomic choice when a passive pipeline still needs the gate to
// suppress unsafe rewrites.
func Wrap(inner optimizer.Optimizer, threshold float64) optimizer.Optimizer {
	if threshold <= 0 {
		threshold = DefaultThreshold
	}
	return &gatedOptimizer{inner: inner, threshold: threshold}
}

type gatedOptimizer struct {
	inner     optimizer.Optimizer
	threshold float64
}

func (g *gatedOptimizer) Kind() eventschema.OptimizationType { return g.inner.Kind() }

func (g *gatedOptimizer) Run(ctx context.Context, req *optimizer.Request) ([]optimizer.Recommendation, error) {
	recs, err := g.inner.Run(ctx, req)
	if err != nil {
		return nil, err
	}
	for i := range recs {
		if recs[i].QualityScore >= g.threshold {
			continue
		}
		recs[i].ApplyBody = nil
		recs[i].Reason = annotateReason(recs[i].Reason, g.threshold, recs[i].QualityScore)
	}
	return recs, nil
}

func annotateReason(original string, threshold, score float64) string {
	prefix := fmt.Sprintf("%s(score=%.2f, min=%.2f)", reasonPrefix, score, threshold)
	if original == "" {
		return prefix
	}
	return prefix + ": " + original
}
