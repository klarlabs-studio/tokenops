package workflowtrace

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/spend/spend"
	"go.klarlabs.de/tokenops/internal/storage/sqlite"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

func newStore(t *testing.T) *sqlite.Store {
	t.Helper()
	store, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "e.db"), sqlite.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func TestDetailFindsRepeatedPrompts(t *testing.T) {
	store := newStore(t)
	at := time.Date(2026, 1, 5, 10, 0, 0, 0, time.UTC)
	for i, id := range []string{"a", "b"} {
		if err := store.Append(context.Background(), &eventschema.Envelope{
			ID: id, SchemaVersion: eventschema.SchemaVersion, Type: eventschema.EventTypePrompt,
			Timestamp: at.Add(time.Duration(i) * time.Minute), Source: "test",
			Payload: &eventschema.PromptEvent{
				PromptHash: "same", Provider: eventschema.ProviderOpenAI, RequestModel: "gpt-4o-mini",
				InputTokens: 100, OutputTokens: 10, TotalTokens: 110, Status: 200, WorkflowID: "wf",
			},
		}); err != nil {
			t.Fatal(err)
		}
	}
	r := NewReader(store, spend.NewEngine(spend.DefaultTable()), WasteConfig{})
	d, err := r.Detail(context.Background(), "wf")
	if err != nil {
		t.Fatal(err)
	}
	if d.Currency != "USD" || d.Trace == nil || len(d.Trace.Steps) != 2 {
		t.Fatalf("detail = %+v", d)
	}
	if len(d.Findings) == 0 {
		t.Error("two consecutive identical prompts should produce a waste finding")
	}
}

// Find takes the waste configuration per call, so a caller that follows
// config edits sees them on the next answer, and tolerates no spend
// engine.
func TestFindAppliesTheConfigItIsGiven(t *testing.T) {
	store := newStore(t)
	at := time.Date(2026, 1, 5, 10, 0, 0, 0, time.UTC)
	for i, in := range []int64{100, 500} {
		id := string(rune('a' + i))
		if err := store.Append(context.Background(), &eventschema.Envelope{
			ID: id, SchemaVersion: eventschema.SchemaVersion, Type: eventschema.EventTypePrompt,
			Timestamp: at.Add(time.Duration(i) * time.Minute), Source: "test",
			Payload: &eventschema.PromptEvent{
				PromptHash: id, Provider: eventschema.ProviderOpenAI, RequestModel: "gpt-4o-mini",
				InputTokens: in, OutputTokens: 10, TotalTokens: in + 10, Status: 200, WorkflowID: "wf",
			},
		}); err != nil {
			t.Fatal(err)
		}
	}
	quiet, err := Find(context.Background(), store, nil, "wf", WasteConfig{})
	if err != nil {
		t.Fatal(err)
	}
	strict, err := Find(context.Background(), store, nil, "wf", WasteConfig{ContextGrowthLimitTokens: 1})
	if err != nil {
		t.Fatal(err)
	}
	if quiet.Currency != "" || len(strict.Findings) <= len(quiet.Findings) {
		t.Fatalf("quiet = %+v, strict = %+v", quiet.Findings, strict.Findings)
	}
}

func TestDetailOfAnUnknownWorkflow(t *testing.T) {
	r := NewReader(newStore(t), spend.NewEngine(spend.DefaultTable()), WasteConfig{})
	if _, err := r.Detail(context.Background(), "missing"); !errors.Is(err, ErrNoTrace) {
		t.Fatalf("err = %v, want ErrNoTrace", err)
	}
}
