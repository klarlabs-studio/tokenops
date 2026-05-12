package eval

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// bundledTestdataGlob resolves the path of the bundled testdata
// directory relative to this source file using runtime.Caller, so the
// glob works regardless of the caller's working directory.
func bundledTestdataGlob() string {
	_, here, _, ok := runtime.Caller(0)
	if !ok {
		return "internal/contexts/optimization/eval/testdata/*.json"
	}
	return filepath.Join(filepath.Dir(here), "testdata", "*.json")
}

// RunParams bundles everything the harness needs to produce a report.
// Both `tokenops eval` (CLI) and `tokenops_eval` (MCP) construct one of
// these so the application logic — suite loading, pipeline build, suite
// merge, gate evaluation — lives in this package, not in the adapters.
type RunParams struct {
	// Suites is a glob matching one or more JSON suite fixtures. Empty
	// defaults to internal/contexts/optimization/eval/testdata/*.json.
	Suites string
	// BaselinePath, when non-empty, points at a previously persisted
	// Report JSON used by the gate.
	BaselinePath string
	// OptimizerFilters restricts the pipeline to these optimizer types
	// (matched against PipelineBuilder filters). Empty enables all.
	OptimizerFilters []OptimizationType
	// Gate carries the regression thresholds. Zero fields receive
	// package defaults via Gate.withDefaults.
	Gate Gate
}

// RunResult bundles the merged Report and the gate's verdict.
type RunResult struct {
	Suites []*Suite    `json:"-"`
	Report *Report     `json:"report"`
	Gate   *GateResult `json:"gate"`
}

// Run executes the harness end-to-end. CLI and MCP both call this; the
// only adapter responsibilities are flag parsing (CLI) / argument
// unmarshalling (MCP) and output formatting.
func Run(ctx context.Context, params RunParams) (*RunResult, error) {
	suitesGlob := params.Suites
	if suitesGlob == "" {
		suitesGlob = bundledTestdataGlob()
	}
	suites, err := LoadSuites(suitesGlob)
	if err != nil {
		return nil, fmt.Errorf("load suites %q: %w", suitesGlob, err)
	}
	if len(suites) == 0 {
		return nil, fmt.Errorf("no suites matched %q", suitesGlob)
	}
	runner := NewRunner(NewPipelineBuilder(params.OptimizerFilters...).Build())
	merged := &Report{Name: "merged", Optimizers: map[OptimizationType]OptimizerStat{}}
	for _, s := range suites {
		r, err := runner.RunSuite(ctx, s)
		if err != nil {
			return nil, fmt.Errorf("run suite %q: %w", s.Name, err)
		}
		mergeReport(merged, r)
	}
	if merged.TotalCases > 0 {
		merged.SuccessRate = float64(merged.PassedCases) / float64(merged.TotalCases) * 100
	}
	var baseline *Report
	if params.BaselinePath != "" {
		b, err := loadReport(params.BaselinePath)
		if err != nil {
			return nil, fmt.Errorf("load baseline %q: %w", params.BaselinePath, err)
		}
		baseline = b
	}
	gate := params.Gate.Evaluate(baseline, merged)
	return &RunResult{Suites: suites, Report: merged, Gate: gate}, nil
}

// PersistBaseline serializes report to path. Both CLI's --output flag and
// MCP's persistence option delegate here.
func PersistBaseline(path string, report *Report) error {
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func loadReport(path string) (*Report, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var r Report
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

func mergeReport(dst, src *Report) {
	dst.TotalCases += src.TotalCases
	dst.PassedCases += src.PassedCases
	dst.Steps = append(dst.Steps, src.Steps...)
	for k, v := range src.Optimizers {
		cur := dst.Optimizers[k]
		prevWeight := float64(cur.TotalCases)
		newWeight := float64(v.TotalCases)
		cur.TotalCases += v.TotalCases
		cur.PassedCases += v.PassedCases
		cur.AvgQuality = weightedAvg(cur.AvgQuality, prevWeight, v.AvgQuality, newWeight)
		cur.TotalSaved += v.TotalSaved
		cur.ApplyRate = weightedAvg(cur.ApplyRate, prevWeight, v.ApplyRate, newWeight)
		dst.Optimizers[k] = cur
	}
}

func weightedAvg(a, wa, b, wb float64) float64 {
	if wa+wb == 0 {
		return 0
	}
	return (a*wa + b*wb) / (wa + wb)
}
