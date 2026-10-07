package evals

import (
	"context"
	"path/filepath"
	"testing"
)

// A saved report gates the next run: same suites, no drift, pass.
func TestSavedBaselineGatesTheNextRun(t *testing.T) {
	ctx := context.Background()
	first, err := Run(ctx, Params{})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "nested", "baseline.json")
	if err := SaveBaseline(path, first.Report); err != nil {
		t.Fatal(err)
	}
	again, err := Run(ctx, Params{Baseline: path})
	if err != nil {
		t.Fatal(err)
	}
	if !again.Gate.Passed || len(again.Gate.Drift) == 0 {
		t.Errorf("gate %+v, want a pass with per-optimizer drift", again.Gate)
	}
}

func TestRunRestrictsToTheGivenOptimizers(t *testing.T) {
	got, err := Run(context.Background(), Params{Optimizers: []string{"context_trim"}})
	if err != nil {
		t.Fatal(err)
	}
	for k := range got.Report.Optimizers {
		if k != "context_trim" && k != "unknown" {
			t.Errorf("optimizer %q ran, want only context_trim", k)
		}
	}
}
