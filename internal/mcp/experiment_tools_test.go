package mcp

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"go.klarlabs.de/tokenops/internal/capability/experiments"
	"go.klarlabs.de/tokenops/internal/storage/sqlite"
)

func TestExperimentToolRequiresExplicitBoundedEnrollment(t *testing.T) {
	store, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "events.db"), sqlite.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	srv := NewServer("test", "test", nil)
	if err := RegisterExperimentTools(srv, ExperimentDeps{Manager: experiments.New(store)}); err != nil {
		t.Fatal(err)
	}
	var started experimentResult
	out := execTool(t, srv, "tokenops_experiment", map[string]any{
		"action": "start", "provider": "anthropic", "baseline_model": "opus", "variant_model": "sonnet", "max_pairs": 2,
		"objective_metric": "tokens", "min_improvement_pct": 10,
		"guardrails": []map[string]any{{"metric": "quality"}, {"metric": "latency_ms", "max_regression_pct": 10}},
	})
	if err := json.Unmarshal([]byte(out), &started); err != nil {
		t.Fatal(err)
	}
	if started.State == nil || started.State.MaxPairs != 2 {
		t.Fatalf("start = %+v", started)
	}
	var stopped experimentResult
	out = execTool(t, srv, "tokenops_experiment", map[string]any{"action": "stop", "experiment_id": started.State.ID, "reason": "operator stopped"})
	if err := json.Unmarshal([]byte(out), &stopped); err != nil {
		t.Fatal(err)
	}
	if stopped.State == nil || stopped.State.Stage != "stopped" {
		t.Fatalf("stop = %+v", stopped)
	}
}
