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
	if got, want := len(envs), 3*10; got != want {
		t.Fatalf("count=%d want %d", got, want)
	}

	providers := map[eventschema.Provider]bool{}
	models := map[string]bool{}
	totalCost := 0.0
	for _, e := range envs {
		if e.Type != eventschema.EventTypePrompt {
			t.Errorf("type=%s want prompt", e.Type)
		}
		p, ok := e.Payload.(*eventschema.PromptEvent)
		if !ok {
			t.Fatalf("payload not PromptEvent: %T", e.Payload)
		}
		providers[p.Provider] = true
		models[p.RequestModel] = true
		totalCost += p.CostUSD
		if p.InputTokens <= 0 || p.OutputTokens <= 0 {
			t.Errorf("non-positive tokens: in=%d out=%d", p.InputTokens, p.OutputTokens)
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
}

func TestGenerateDemoEnvelopesDeterministic(t *testing.T) {
	a := generateDemoEnvelopes(2, 5, 99)
	b := generateDemoEnvelopes(2, 5, 99)
	if len(a) != len(b) {
		t.Fatalf("len mismatch: %d vs %d", len(a), len(b))
	}
	// IDs are uuid.NewString() so they will differ run-to-run — payload
	// shape is what must be deterministic.
	for i := range a {
		pa := a[i].Payload.(*eventschema.PromptEvent)
		pb := b[i].Payload.(*eventschema.PromptEvent)
		if pa.InputTokens != pb.InputTokens || pa.OutputTokens != pb.OutputTokens || pa.CostUSD != pb.CostUSD {
			t.Errorf("non-deterministic at %d: %+v vs %+v", i, pa, pb)
			break
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
	if !strings.Contains(out.String(), "seeded 60 events") {
		t.Errorf("expected seeded count in output, got: %s", out.String())
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
		t.Fatalf("count: %v", err)
	}
	if n != 60 {
		t.Errorf("store count=%d want 60", n)
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
	if !strings.Contains(out.String(), "dry-run: would seed 10 events") {
		t.Errorf("expected dry-run summary, got: %s", out.String())
	}
	if _, err := os.Stat(storagePath); err == nil {
		t.Errorf("dry-run created the store at %s", storagePath)
	}
}
