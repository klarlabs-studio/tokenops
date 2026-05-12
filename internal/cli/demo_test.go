package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/felixgeelhaar/tokenops/internal/storage/sqlite"
	"github.com/felixgeelhaar/tokenops/pkg/eventschema"
)

func TestGenerateDemoEnvelopesShape(t *testing.T) {
	envs := generateDemoEnvelopes(3, 10, 42)
	prompts, optimizations := countDemoPayloads(envs)
	if prompts != 3*10 {
		t.Fatalf("prompts=%d want %d", prompts, 3*10)
	}
	if optimizations <= 0 {
		t.Errorf("expected >0 optimization events, got %d", optimizations)
	}
	if len(envs) != prompts+optimizations {
		t.Errorf("len(envs)=%d != prompts+optimizations=%d", len(envs), prompts+optimizations)
	}

	providers := map[eventschema.Provider]bool{}
	models := map[string]bool{}
	totalCost := 0.0
	totalSaved := int64(0)
	for _, e := range envs {
		switch payload := e.Payload.(type) {
		case *eventschema.PromptEvent:
			providers[payload.Provider] = true
			models[payload.RequestModel] = true
			totalCost += payload.CostUSD
			if payload.InputTokens <= 0 || payload.OutputTokens <= 0 {
				t.Errorf("non-positive tokens: in=%d out=%d", payload.InputTokens, payload.OutputTokens)
			}
		case *eventschema.OptimizationEvent:
			totalSaved += payload.EstimatedSavingsTokens
			if payload.Decision != eventschema.OptimizationDecisionApplied {
				t.Errorf("decision=%s want applied", payload.Decision)
			}
			if payload.QualityScore < 0.7 {
				t.Errorf("quality_score=%f below realistic floor", payload.QualityScore)
			}
		default:
			t.Errorf("unexpected payload type %T", e.Payload)
		}
		if e.ID == "" {
			t.Error("empty envelope ID")
		}
	}
	if len(providers) < 2 {
		t.Errorf("expected events spread across providers, got %v", providers)
	}
	if len(models) < 3 {
		t.Errorf("expected diverse models, got %v", models)
	}
	if totalCost <= 0 {
		t.Errorf("totalCost=%f want positive", totalCost)
	}
	if totalSaved <= 0 {
		t.Errorf("totalSaved=%d want positive so TEU lifts off zero", totalSaved)
	}
}

func TestGenerateDemoEnvelopesDeterministic(t *testing.T) {
	a := generateDemoEnvelopes(2, 5, 99)
	b := generateDemoEnvelopes(2, 5, 99)
	if len(a) != len(b) {
		t.Fatalf("len mismatch: %d vs %d", len(a), len(b))
	}
	// IDs are uuid.NewString() so they will differ run-to-run — payload
	// shape is what must be deterministic. Compare per envelope type so
	// the mixed PromptEvent + OptimizationEvent stream stays diffable.
	for i := range a {
		if a[i].Type != b[i].Type {
			t.Fatalf("type mismatch at %d: %s vs %s", i, a[i].Type, b[i].Type)
		}
		switch pa := a[i].Payload.(type) {
		case *eventschema.PromptEvent:
			pb := b[i].Payload.(*eventschema.PromptEvent)
			if pa.InputTokens != pb.InputTokens || pa.OutputTokens != pb.OutputTokens || pa.CostUSD != pb.CostUSD {
				t.Errorf("non-deterministic prompt at %d: %+v vs %+v", i, pa, pb)
				return
			}
		case *eventschema.OptimizationEvent:
			pb := b[i].Payload.(*eventschema.OptimizationEvent)
			if pa.EstimatedSavingsTokens != pb.EstimatedSavingsTokens || pa.Kind != pb.Kind {
				t.Errorf("non-deterministic optimization at %d: %+v vs %+v", i, pa, pb)
				return
			}
		}
	}
}

func TestDemoSeedsStoreAndQueriesNonZero(t *testing.T) {
	dir := t.TempDir()
	storagePath := filepath.Join(dir, "events.db")

	cmd := newDemoCmd()
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs([]string{
		"--storage-path", storagePath,
		"--days", "3",
		"--per-day", "20",
		"--seed", "7",
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("demo: %v\noutput: %s", err, out.String())
	}
	if !strings.Contains(out.String(), "60 prompts +") {
		t.Errorf("expected prompt count in output, got: %s", out.String())
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	store, err := sqlite.Open(ctx, storagePath, sqlite.Options{})
	if err != nil {
		t.Fatalf("open seeded store: %v", err)
	}
	defer func() { _ = store.Close() }()

	n, err := store.Count(ctx, sqlite.Filter{Type: eventschema.EventTypePrompt})
	if err != nil {
		t.Fatalf("count prompts: %v", err)
	}
	if n != 60 {
		t.Errorf("store prompt count=%d want 60", n)
	}
	optN, err := store.Count(ctx, sqlite.Filter{Type: eventschema.EventTypeOptimization})
	if err != nil {
		t.Fatalf("count optimizations: %v", err)
	}
	if optN <= 0 {
		t.Errorf("expected >0 optimization events seeded, got %d", optN)
	}
}

func TestDemoDryRunDoesNotWrite(t *testing.T) {
	dir := t.TempDir()
	storagePath := filepath.Join(dir, "events.db")

	cmd := newDemoCmd()
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs([]string{
		"--storage-path", storagePath,
		"--days", "2",
		"--per-day", "5",
		"--dry-run",
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("demo dry-run: %v", err)
	}
	if !strings.Contains(out.String(), "dry-run: would seed") || !strings.Contains(out.String(), "10 prompts +") {
		t.Errorf("expected dry-run summary with prompt count, got: %s", out.String())
	}
	if _, err := os.Stat(storagePath); err == nil {
		t.Errorf("dry-run created the store at %s", storagePath)
	}
}
