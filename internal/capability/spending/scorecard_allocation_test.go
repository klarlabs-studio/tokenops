package spending

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/allocation"
	"go.klarlabs.de/tokenops/internal/storage/sqlite"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// With allocation rules, the scorecard shows how much of the period's
// spend they tag, on every surface that asks Scorecard.
func TestScorecardShowsTaggedSpend(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	ctx := context.Background()
	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "e.db"), sqlite.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Now().UTC()
	turn := func(id, source string, usd float64) *eventschema.Envelope {
		return &eventschema.Envelope{
			ID: id, SchemaVersion: eventschema.SchemaVersion, Type: eventschema.EventTypePrompt,
			Timestamp: now.Add(-time.Hour), Source: source,
			Payload: &eventschema.PromptEvent{Provider: eventschema.ProviderOpenAI, RequestModel: "gpt-5",
				SessionID: "s-" + id, InputTokens: 100, OutputTokens: 10, TotalTokens: 110, CostUSD: usd},
		}
	}
	if err := store.AppendBatch(ctx, []*eventschema.Envelope{turn("a", "codex-jsonl", 3), turn("b", "claude-code-jsonl", 1)}); err != nil {
		t.Fatal(err)
	}
	eng := NewEngine(DefaultTable())
	rules := []allocation.Rule{{Match: allocation.Match{Harness: "codex"}, Tags: allocation.Tags{CostCenter: "cc-1"}}}
	s := Scorecard(ctx, store, ScorecardParams{Spend: eng, Allocation: rules, Now: now})
	if s.Allocation == nil || s.Allocation.Rules != 1 || s.Allocation.TaggedPct != 75 || s.Allocation.ByKeyPct["cost_center"] != 75 {
		t.Fatalf("allocation = %+v", s.Allocation)
	}
	if none := Scorecard(ctx, store, ScorecardParams{Spend: eng, Now: now}); none.Allocation != nil {
		t.Errorf("without rules, allocation = %+v", none.Allocation)
	}
}
