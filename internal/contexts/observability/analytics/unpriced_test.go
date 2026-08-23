package analytics

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/spend/spend"
	"go.klarlabs.de/tokenops/internal/storage/sqlite"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

func storeWith(t *testing.T, envs ...*eventschema.Envelope) *sqlite.Store {
	t.Helper()
	st, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "e.db"), sqlite.Options{})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.AppendBatch(context.Background(), envs); err != nil {
		t.Fatalf("append: %v", err)
	}
	return st
}

func planEvent(id, model string, in, out int64) *eventschema.Envelope {
	return &eventschema.Envelope{
		ID:            id,
		SchemaVersion: eventschema.SchemaVersion,
		Type:          eventschema.EventTypePrompt,
		Timestamp:     time.Now().UTC().Add(-time.Hour),
		Payload: &eventschema.PromptEvent{
			Provider: eventschema.ProviderAnthropic, RequestModel: model,
			InputTokens: in, OutputTokens: out, TotalTokens: in + out,
			CostSource: eventschema.CostSourcePlanIncluded,
		},
	}
}

// A plan-covered model with no rate card is invisible twice over: its
// real cost is legitimately zero, and the API-equivalent shadow value
// silently drops it because the pricing error is discarded. On a
// subscription that is the majority of traffic, so the headline
// "api equivalent" figure understates by whatever the unpriced model
// consumed — with no warning anywhere.
func TestUnpricedPlanCoveredModelIsReported(t *testing.T) {
	st := storeWith(t,
		planEvent("a", "claude-sonnet-4-6", 1_000_000, 100_000),
		planEvent("b", "totally-unknown-model", 5_000_000, 500_000),
	)
	agg := New(st, spend.NewEngine(spend.DefaultTable()))

	got, err := agg.Summarize(context.Background(), Filter{Since: time.Now().Add(-24 * time.Hour)})
	if err != nil {
		t.Fatalf("summarize: %v", err)
	}
	var found bool
	for _, u := range got.Unpriced {
		if u.Model == "totally-unknown-model" {
			found = true
			if u.Requests == 0 {
				t.Errorf("unpriced entry has no request count: %+v", u)
			}
		}
	}
	if !found {
		t.Errorf("unpriced plan-covered model not reported; Unpriced=%+v", got.Unpriced)
	}
	if got.APIEquivalentUSD <= 0 {
		t.Errorf("APIEquivalentUSD = %f, want the priced model's value", got.APIEquivalentUSD)
	}
}

// A fully priced plan-covered window reports no gaps.
func TestPricedPlanCoveredModelReportsNoGap(t *testing.T) {
	st := storeWith(t, planEvent("a", "claude-sonnet-4-6", 1_000_000, 100_000))
	agg := New(st, spend.NewEngine(spend.DefaultTable()))
	got, err := agg.Summarize(context.Background(), Filter{Since: time.Now().Add(-24 * time.Hour)})
	if err != nil {
		t.Fatalf("summarize: %v", err)
	}
	if len(got.Unpriced) != 0 {
		t.Errorf("Unpriced = %+v, want none", got.Unpriced)
	}
}
