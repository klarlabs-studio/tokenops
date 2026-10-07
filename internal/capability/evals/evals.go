// Package evals runs the optimizer eval harness: the bundled or given
// suites through the optimizer pipeline, graded per optimizer and gated
// against a baseline report.
package evals

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"go.klarlabs.de/tokenops/internal/contexts/optimization/eval"
)

// Params selects the suites, the baseline and the gate's thresholds.
type Params struct {
	// Suites is a glob of JSON suite fixtures; empty runs the bundled ones.
	Suites string
	// Baseline is a previous report the gate compares against; empty
	// gates on case count alone.
	Baseline string
	// Optimizers restricts the pipeline to these kinds; empty runs all.
	Optimizers []string
	// MaxSuccessDropPct, MaxQualityDriftPct and MinCases are the gate's
	// thresholds; zero takes the harness defaults.
	MaxSuccessDropPct, MaxQualityDriftPct float64
	MinCases                              int
}

// Result is a run: the suites it read, the merged report and the gate.
type Result = eval.RunResult

// Report is the merged per-optimizer quality report.
type Report = eval.Report

// GateResult is the gate's verdict against the baseline.
type GateResult = eval.GateResult

// Suite is one fixture file's cases.
type Suite = eval.Suite

// Run runs the harness.
func Run(ctx context.Context, p Params) (*Result, error) {
	kinds := make([]eval.OptimizationType, 0, len(p.Optimizers))
	for _, k := range p.Optimizers {
		kinds = append(kinds, eval.OptimizationType(k))
	}
	return eval.Run(ctx, eval.RunParams{
		Suites:           p.Suites,
		BaselinePath:     p.Baseline,
		OptimizerFilters: kinds,
		Gate: eval.Gate{
			MaxSuccessRateDropPct: p.MaxSuccessDropPct,
			MaxQualityDriftPct:    p.MaxQualityDriftPct,
			MinTotalCases:         p.MinCases,
		},
	})
}

// SaveBaseline writes r to path, creating its directory, so a later run
// can gate against it.
func SaveBaseline(path string, r *Report) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("write report: %w", err)
	}
	if err := eval.PersistBaseline(path, r); err != nil {
		return fmt.Errorf("write report: %w", err)
	}
	return nil
}

// SuiteNames lists the suites' names, comma-separated.
func SuiteNames(suites []*Suite) string { return eval.SuiteNames(suites) }
