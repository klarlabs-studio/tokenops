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

func TestDetailOfAnUnknownWorkflow(t *testing.T) {
	r := NewReader(newStore(t), spend.NewEngine(spend.DefaultTable()), WasteConfig{})
	if _, err := r.Detail(context.Background(), "missing"); !errors.Is(err, ErrNoTrace) {
		t.Fatalf("err = %v, want ErrNoTrace", err)
	}
}
