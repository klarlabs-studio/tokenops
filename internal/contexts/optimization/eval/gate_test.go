package eval

import (
	"testing"
)

func reportWith(success float64, optAvg map[OptimizationType]float64, totalCases int) *Report {
	r := &Report{
		Name:        "test",
		TotalCases:  totalCases,
		PassedCases: totalCases,
		SuccessRate: success,
		Optimizers:  map[OptimizationType]OptimizerStat{},
	}
	for k, v := range optAvg {
		r.Optimizers[k] = OptimizerStat{TotalCases: 1, PassedCases: 1, AvgQuality: v}
	}
	return r
}

func TestGateAcceptsStableRun(t *testing.T) {
	baseline := reportWith(95.0, map[OptimizationType]float64{
		TypePromptCompress: 0.85,
	}, 10)
	current := reportWith(95.0, map[OptimizationType]float64{
		TypePromptCompress: 0.85,
	}, 10)
	g := Gate{}
	res := g.Evaluate(baseline, current)
	if !res.Passed {
		t.Errorf("expected pass, got violations: %+v", res.Violations)
	}
}

func TestGateRejectsSuccessRateDrop(t *testing.T) {
	baseline := reportWith(95.0, map[OptimizationType]float64{TypePromptCompress: 0.85}, 10)
	current := reportWith(80.0, map[OptimizationType]float64{TypePromptCompress: 0.85}, 10)
	res := Gate{MaxSuccessRateDropPct: 5.0}.Evaluate(baseline, current)
	if res.Passed {
		t.Fatalf("expected gate to fail")
	}
	found := false
	for _, v := range res.Violations {
		if v.Kind == "success_rate_drop" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected success_rate_drop violation: %+v", res.Violations)
	}
}

func TestGateRejectsQualityRegression(t *testing.T) {
	baseline := reportWith(95.0, map[OptimizationType]float64{TypePromptCompress: 0.80}, 10)
	current := reportWith(95.0, map[OptimizationType]float64{TypePromptCompress: 0.60}, 10)
	res := Gate{MaxQualityDriftPct: 10.0}.Evaluate(baseline, current)
	if res.Passed {
		t.Fatalf("expected gate to fail")
	}
	hit := false
	for _, v := range res.Violations {
		if v.Kind == "quality_drift" && v.Optimizer == string(TypePromptCompress) {
			hit = true
		}
	}
	if !hit {
		t.Errorf("expected quality_drift violation: %+v", res.Violations)
	}
	if res.Drift[TypePromptCompress] >= 0 {
		t.Errorf("expected negative drift, got %f", res.Drift[TypePromptCompress])
	}
}

func TestGateRejectsMissingOptimizer(t *testing.T) {
	baseline := reportWith(95.0, map[OptimizationType]float64{
		TypePromptCompress: 0.85,
		TypeSemanticDedupe: 0.80,
	}, 10)
	current := reportWith(95.0, map[OptimizationType]float64{
		TypePromptCompress: 0.85,
	}, 10)
	res := Gate{}.Evaluate(baseline, current)
	if res.Passed {
		t.Fatalf("expected gate to fail")
	}
	hit := false
	for _, v := range res.Violations {
		if v.Kind == "optimizer_disappeared" && v.Optimizer == string(TypeSemanticDedupe) {
			hit = true
		}
	}
	if !hit {
		t.Errorf("expected optimizer_disappeared violation: %+v", res.Violations)
	}
}

func TestGateUnderCovered(t *testing.T) {
	current := reportWith(100.0, map[OptimizationType]float64{}, 0)
	res := Gate{MinTotalCases: 5}.Evaluate(nil, current)
	if res.Passed {
		t.Fatalf("expected under_covered violation, got pass")
	}
	if res.Violations[0].Kind != "under_covered" {
		t.Errorf("kind = %q, want under_covered", res.Violations[0].Kind)
	}
}

func TestGatePassesFirstRunWithoutBaseline(t *testing.T) {
	current := reportWith(80.0, map[OptimizationType]float64{TypePromptCompress: 0.5}, 10)
	res := Gate{MinTotalCases: 1}.Evaluate(nil, current)
	if !res.Passed {
		t.Errorf("expected first-run pass without baseline, got %+v", res.Violations)
	}
}
