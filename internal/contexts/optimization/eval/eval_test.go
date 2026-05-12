package eval_test

import (
	"context"
	"testing"

	"github.com/felixgeelhaar/tokenops/internal/contexts/optimization/eval"
)

const testSuitesGlob = "testdata/*_suite.json"

func TestEvalSuitesPass(t *testing.T) {
	suites, err := eval.LoadSuites(testSuitesGlob)
	if err != nil {
		t.Fatalf("load suites: %v", err)
	}

	if len(suites) == 0 {
		t.Fatal("no eval suites loaded")
	}

	ctx := context.Background()
	builder := eval.NewPipelineBuilder()
	pipeline := builder.Build()
	runner := eval.NewRunner(pipeline)

	var totalCases, totalPassed int
	var suiteFailures []string

	for _, suite := range suites {
		t.Run(suite.Name, func(t *testing.T) {
			report, err := runner.RunSuite(ctx, suite)
			if err != nil {
				t.Fatalf("run suite %s: %v", suite.Name, err)
			}

			t.Logf("Suite: %s (%s)", suite.Name, suite.Description)
			t.Logf("  Cases:   %d total, %d passed, %d failed",
				report.TotalCases, report.PassedCases, report.TotalCases-report.PassedCases)
			t.Logf("  Success: %.1f%%", report.SuccessRate)

			for opt, stat := range report.Optimizers {
				t.Logf("  [%s] %d/%d passed, avg quality %.2f, apply rate %.0f%%",
					opt, stat.PassedCases, stat.TotalCases, stat.AvgQuality, stat.ApplyRate)
			}

			for _, step := range report.Steps {
				if !step.Passed {
					t.Logf("  FAIL: %s/%s - quality=%.2f savings=%d reason=%q",
						step.CaseID, step.Optimizer, step.Quality, step.SavingsTok, step.Reason)
				}
			}

			minSuccessRate := 50.0
			if report.SuccessRate < minSuccessRate {
				suiteFailures = append(suiteFailures, suite.Name)
				t.Errorf("suite %s success rate %.1f%% < %.0f%% minimum",
					suite.Name, report.SuccessRate, minSuccessRate)
			}

			totalCases += report.TotalCases
			totalPassed += report.PassedCases
		})
	}

	if len(suiteFailures) > 0 {
		t.Errorf("%d suite(s) below minimum success rate: %v", len(suiteFailures), suiteFailures)
	}
	t.Logf("Overall: %d/%d cases passed (%.1f%%)", totalPassed, totalCases,
		float64(totalPassed)/float64(totalCases)*100)
}
