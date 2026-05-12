package mcp

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/felixgeelhaar/tokenops/internal/contexts/observability/analytics"
	"github.com/felixgeelhaar/tokenops/internal/contexts/spend/spend"
	"github.com/felixgeelhaar/tokenops/internal/storage/sqlite"
	"github.com/felixgeelhaar/tokenops/pkg/eventschema"
)

func TestOptimizationsMCPTool(t *testing.T) {
	store, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "x.db"), sqlite.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	now := time.Now().UTC()
	for i, k := range []eventschema.OptimizationType{
		eventschema.OptimizationTypePromptCompress,
		eventschema.OptimizationTypeDedupe,
	} {
		env := &eventschema.Envelope{
			ID:            "opt-" + string(rune('a'+i)),
			SchemaVersion: eventschema.SchemaVersion,
			Type:          eventschema.EventTypeOptimization,
			Timestamp:     now.Add(time.Duration(i) * time.Second),
			Payload: &eventschema.OptimizationEvent{
				PromptHash:             "sha256:abc",
				Kind:                   k,
				Mode:                   eventschema.OptimizationModePassive,
				Decision:               eventschema.OptimizationDecisionApplied,
				EstimatedSavingsTokens: int64(100 * (i + 1)),
			},
		}
		if err := store.Append(context.Background(), env); err != nil {
			t.Fatalf("append: %v", err)
		}
	}

	spendEng := spend.NewEngine(spend.DefaultTable())
	srv := NewServer("tokenops", "test", slog.New(slog.NewTextHandler(os.Stderr, nil)))
	if err := RegisterTools(srv, Deps{
		Store:      store,
		Aggregator: analytics.New(store, spendEng),
		Spend:      spendEng,
	}); err != nil {
		t.Fatal(err)
	}
	out := execTool(t, srv, "tokenops_optimizations", map[string]any{"limit": 10})
	for _, want := range []string{
		`"optimizations"`,
		`"prompt_compress"`,
		`"semantic_dedupe"`,
		`"estimated_savings_tokens"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in output:\n%s", want, out)
		}
	}
}
