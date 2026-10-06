package analytics

import (
	"context"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/spend/spend"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// A plan-window reading — no model, no tokens — is neither a request nor
// an unpriced model; usage with tokens and no model is both, because it is
// a real gap.
func TestReadingsAreNotRequests(t *testing.T) {
	ctx := context.Background()
	store := newStore(t)
	now := time.Now().UTC()
	reading := mkPrompt("r1", now.Add(-time.Hour), "", 0, 0, 0)
	reading.Source = "claude-usage-meter"
	usage := mkPrompt("u1", now.Add(-time.Hour), "gpt-6-sol", 1000, 100, 0)
	nameless := mkPrompt("n1", now.Add(-time.Hour), "", 500, 50, 0)
	if err := store.AppendBatch(ctx, []*eventschema.Envelope{reading, usage, nameless}); err != nil {
		t.Fatal(err)
	}
	agg := New(store, spend.NewEngine(spend.DefaultTable()))
	s, err := agg.Summarize(ctx, Filter{Since: now.Add(-24 * time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if s.Requests != 2 {
		t.Errorf("requests = %d, want 2: the reading counted", s.Requests)
	}
	if len(s.Unpriced) != 1 || s.Unpriced[0].Model != "" || s.Unpriced[0].Requests != 1 {
		t.Errorf("unpriced = %+v, want only the nameless usage", s.Unpriced)
	}
}
