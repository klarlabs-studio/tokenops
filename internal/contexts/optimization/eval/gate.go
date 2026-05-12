package eval

import (
	"fmt"
	"slices"
)

// Gate is the regression gate that compares a current eval Report against
// a stored baseline and decides whether the run is acceptable. The gate
// is enforced both by the CLI (tokenops eval --enforce) and by CI; the
// thresholds are documented in docs/telemetry-contracts.md so any change
// requires a contract review.
type Gate struct {
	// MaxSuccessRateDropPct rejects runs whose suite-wide success rate
	// drops by more than this many absolute percentage points vs baseline.
	// Default 5.0.
	MaxSuccessRateDropPct float64
	// MaxQualityDriftPct rejects runs where any per-optimizer AvgQuality
	// regresses by more than this many percentage points (relative).
	// Default 10.0.
	MaxQualityDriftPct float64
	// MinTotalCases guards against under-covered runs slipping through
	// the gate. Reports with fewer than this many cases fail open with
	// a violation. Default 1.
	MinTotalCases int
}

// withDefaults returns g with zero fields replaced by package defaults.
func (g Gate) withDefaults() Gate {
	if g.MaxSuccessRateDropPct == 0 {
		g.MaxSuccessRateDropPct = 5.0
	}
	if g.MaxQualityDriftPct == 0 {
		g.MaxQualityDriftPct = 10.0
	}
	if g.MinTotalCases == 0 {
		g.MinTotalCases = 1
	}
	return g
}

// GateViolation describes one failed gate check.
type GateViolation struct {
	Kind      string  `json:"kind"`
	Optimizer string  `json:"optimizer,omitempty"`
	Detail    string  `json:"detail"`
	Observed  float64 `json:"observed"`
	Limit     float64 `json:"limit"`
}

// GateResult bundles gate outcome.
type GateResult struct {
	Passed     bool            `json:"passed"`
	Violations []GateViolation `json:"violations,omitempty"`
	// Drift maps optimizer name to relative AvgQuality drift (percent).
	// Negative values are regressions.
	Drift map[OptimizationType]float64 `json:"drift,omitempty"`
}

// Evaluate runs the gate. baseline may be nil; when nil, only absolute
// floors (MinTotalCases) are enforced — useful for first runs.
func (g Gate) Evaluate(baseline, current *Report) *GateResult {
	g = g.withDefaults()
	res := &GateResult{Passed: true, Drift: map[OptimizationType]float64{}}
	if current == nil {
		res.Passed = false
		res.Violations = append(res.Violations, GateViolation{
			Kind: "missing_current", Detail: "current report is nil",
		})
		return res
	}
	if current.TotalCases < g.MinTotalCases {
		res.Passed = false
		res.Violations = append(res.Violations, GateViolation{
			Kind: "under_covered",
			Detail: fmt.Sprintf("only %d cases ran, gate requires >= %d",
				current.TotalCases, g.MinTotalCases),
			Observed: float64(current.TotalCases),
			Limit:    float64(g.MinTotalCases),
		})
	}
	if baseline == nil {
		return res
	}
	drop := baseline.SuccessRate - current.SuccessRate
	if drop > g.MaxSuccessRateDropPct {
		res.Passed = false
		res.Violations = append(res.Violations, GateViolation{
			Kind:     "success_rate_drop",
			Detail:   fmt.Sprintf("success rate dropped by %.2f pp", drop),
			Observed: drop,
			Limit:    g.MaxSuccessRateDropPct,
		})
	}
	// Walk optimizers in sorted order so violations are deterministic.
	keys := make([]OptimizationType, 0, len(baseline.Optimizers))
	for k := range baseline.Optimizers {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		bStat := baseline.Optimizers[k]
		cStat, ok := current.Optimizers[k]
		if !ok {
			res.Passed = false
			res.Violations = append(res.Violations, GateViolation{
				Kind:      "optimizer_disappeared",
				Optimizer: string(k),
				Detail:    "optimizer present in baseline but absent in current run",
			})
			continue
		}
		if bStat.AvgQuality == 0 {
			continue
		}
		driftPct := (cStat.AvgQuality - bStat.AvgQuality) / bStat.AvgQuality * 100
		res.Drift[k] = driftPct
		if driftPct < -g.MaxQualityDriftPct {
			res.Passed = false
			res.Violations = append(res.Violations, GateViolation{
				Kind:      "quality_drift",
				Optimizer: string(k),
				Detail: fmt.Sprintf("AvgQuality regressed %.2f%% (baseline %.3f → current %.3f)",
					driftPct, bStat.AvgQuality, cStat.AvgQuality),
				Observed: driftPct,
				Limit:    -g.MaxQualityDriftPct,
			})
		}
	}
	return res
}
