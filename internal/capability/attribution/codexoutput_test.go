package attribution

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/security/audit"
	"go.klarlabs.de/tokenops/internal/storage/sqlite"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// TestCorrectCodexOutputAuditsOnce: a Codex turn stored by a release that
// counted reasoning twice is corrected and the correction audited; a
// second pass changes and audits nothing.
func TestCorrectCodexOutputAuditsOnce(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "events.db"), sqlite.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.Append(ctx, &eventschema.Envelope{
		ID: "cdx-old", SchemaVersion: eventschema.SchemaVersion, Type: eventschema.EventTypePrompt,
		Timestamp: time.Now().Add(-time.Hour), Source: "codex-jsonl",
		Attributes: map[string]string{"reasoning_output": "30"},
		Payload: &eventschema.PromptEvent{
			Provider: eventschema.ProviderOpenAI, RequestModel: "gpt-5.5",
			InputTokens: 100, OutputTokens: 80, TotalTokens: 180,
		},
	}); err != nil {
		t.Fatal(err)
	}

	for start := 1; start <= 2; start++ {
		n, cerr := CorrectCodexOutput(ctx, store)
		if cerr != nil {
			t.Fatal(cerr)
		}
		if want := int64(2 - start); n != want {
			t.Errorf("pass %d corrected %d, want %d", start, n, want)
		}
		envs, err := store.Query(ctx, sqlite.Filter{})
		if err != nil {
			t.Fatal(err)
		}
		if p := envs[0].Payload.(*eventschema.PromptEvent); p.OutputTokens != 50 || p.TotalTokens != 150 {
			t.Errorf("pass %d: output/total = %d/%d, want 50/150", start, p.OutputTokens, p.TotalTokens)
		}
		entries, err := audit.NewRecorder(store).Query(ctx, audit.Filter{Action: audit.ActionCostCorrection})
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != 1 {
			t.Fatalf("pass %d: %d audit entries, want 1", start, len(entries))
		}
		if got := entries[0].Details["calls"]; got != float64(1) { // JSON numbers decode as float64
			t.Errorf("audited calls = %v (%T), want 1", got, got)
		}
	}
}
