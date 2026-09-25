package mcp

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/coaching/waste"
	"go.klarlabs.de/tokenops/internal/contexts/observability/analytics"
	"go.klarlabs.de/tokenops/internal/contexts/spend/spend"
	"go.klarlabs.de/tokenops/internal/storage/sqlite"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

func TestReviewWorkComposesTraceUsageAndFindings(t *testing.T) {
	store, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "review.db"), sqlite.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	const workflowID = "workflow-review"
	for i, inputTokens := range []int64{100, 500} {
		prompt := &eventschema.PromptEvent{
			Provider: eventschema.ProviderAnthropic, RequestModel: "claude-sonnet-4-5",
			InputTokens: inputTokens, OutputTokens: 25, TotalTokens: inputTokens + 25,
			WorkflowID: workflowID,
		}
		env := &eventschema.Envelope{
			ID: "review-" + string(rune('a'+i)), SchemaVersion: eventschema.SchemaVersion,
			Type: eventschema.EventTypePrompt, Timestamp: time.Now().UTC().Add(time.Duration(i) * time.Second),
			Source: "test", Payload: prompt,
		}
		if err := store.Append(context.Background(), env); err != nil {
			t.Fatalf("append prompt: %v", err)
		}
	}

	eng := spend.NewEngine(spend.DefaultTable())
	srv := NewServer("tokenops", "test", slog.New(slog.NewTextHandler(os.Stderr, nil)))
	if err := RegisterTools(srv, Deps{Store: store, Aggregator: analytics.New(store, eng), Spend: eng}); err != nil {
		t.Fatal(err)
	}
	out := execTool(t, srv, "tokenops_review_work", map[string]any{"workflow_id": workflowID})
	for _, want := range []string{`"workflow_id": "workflow-review"`, `"step_count": 2`, `"total_tokens": 650`, `"context_growth_tokens": 400`, `"findings": []`, `"level": "no_finding"`, `this is not an assessment of task success or overall work quality`} {
		if !strings.Contains(out, want) {
			t.Errorf("review result missing %s:\n%s", want, out)
		}
	}
	if strings.Contains(out, `"prompt_hash"`) {
		t.Errorf("aggregate work review included per-prompt content:\n%s", out)
	}
}

func TestReviewWorkInsightReflectsExistingWasteFinding(t *testing.T) {
	store, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "review.db"), sqlite.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	const workflowID = "workflow-growth"
	for i, inputTokens := range []int64{100, 500} {
		p := &eventschema.PromptEvent{Provider: eventschema.ProviderAnthropic, RequestModel: "claude-sonnet-4-5", InputTokens: inputTokens, OutputTokens: 25, TotalTokens: inputTokens + 25, WorkflowID: workflowID}
		env := &eventschema.Envelope{ID: "growth-" + string(rune('a'+i)), SchemaVersion: eventschema.SchemaVersion, Type: eventschema.EventTypePrompt, Timestamp: time.Now().UTC().Add(time.Duration(i) * time.Second), Source: "test", Payload: p}
		if err := store.Append(context.Background(), env); err != nil {
			t.Fatalf("append prompt: %v", err)
		}
	}
	eng := spend.NewEngine(spend.DefaultTable())
	srv := NewServer("tokenops", "test", slog.New(slog.NewTextHandler(os.Stderr, nil)))
	if err := RegisterTools(srv, Deps{Store: store, Aggregator: analytics.New(store, eng), Spend: eng, Waste: waste.Config{ContextGrowthLimitTokens: 1}}); err != nil {
		t.Fatal(err)
	}
	out := execTool(t, srv, "tokenops_review_work", map[string]any{"workflow_id": workflowID})
	for _, want := range []string{`"level": "attention"`, `"finding_count": 1`, `Runaway context growth`} {
		if !strings.Contains(out, want) {
			t.Errorf("review result missing %s:\n%s", want, out)
		}
	}
}
