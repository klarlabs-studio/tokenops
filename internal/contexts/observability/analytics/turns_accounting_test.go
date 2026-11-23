package analytics_test

import (
	"context"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/observability/analytics"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// A turn carries the reader that recorded it, whether a plan covered it
// and its input and output tokens, so allocation rules can match its
// harness and a billing export can split its charge by tag.
func TestSessionTurnsCarrySourceAndAccounting(t *testing.T) {
	covered := planEvent("c", "claude-sonnet-5", 1_000, 200)
	covered.Source = "claude-code-jsonl"
	covered.Payload.(*eventschema.PromptEvent).SessionID = "s-1"
	metered := &eventschema.Envelope{
		ID: "m", SchemaVersion: eventschema.SchemaVersion, Type: eventschema.EventTypePrompt,
		Timestamp: time.Now().UTC().Add(-time.Hour), Source: "codex-jsonl",
		Payload: &eventschema.PromptEvent{
			Provider: eventschema.ProviderOpenAI, RequestModel: "gpt-5", SessionID: "s-2",
			InputTokens: 50, OutputTokens: 5, TotalTokens: 55, CostUSD: 0.01,
		},
	}
	turns, err := analytics.New(storeWith(t, covered, metered), nil).SessionTurns(context.Background(),
		analytics.Filter{Since: time.Now().Add(-24 * time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if len(turns) != 2 {
		t.Fatalf("turns = %+v", turns)
	}
	bySession := map[string]analytics.SessionTurn{}
	for _, tn := range turns {
		bySession[tn.SessionID] = tn
	}
	if c := bySession["s-1"]; c.Source != "claude-code-jsonl" || !c.PlanCovered || c.InputTokens != 1_000 || c.OutputTokens != 200 {
		t.Errorf("covered turn = %+v", c)
	}
	if m := bySession["s-2"]; m.Source != "codex-jsonl" || m.PlanCovered || m.InputTokens != 50 || m.OutputTokens != 5 {
		t.Errorf("metered turn = %+v", m)
	}
}

// usageOnly is a store that answers SessionUsage with rows, and nothing
// else.
type usageOnly struct {
	analytics.Store
	rows []analytics.SessionUsage
}

func (s usageOnly) SessionUsage(context.Context, analytics.Filter) ([]analytics.SessionUsage, error) {
	return s.rows, nil
}

// The turns are built into one slice of their number: grown by appending,
// six weeks of a busy machine's turns (the team upload's read) held up to
// twice their 125 MB while the slice was copied into larger ones.
func TestSessionTurnsAllocateOneSlice(t *testing.T) {
	rows := make([]analytics.SessionUsage, 10_000)
	for i := range rows {
		rows[i] = analytics.SessionUsage{SessionID: "s", Provider: "openai", Model: "gpt-5", TotalTokens: 10, CostUSD: 0.01}
	}
	agg := analytics.New(usageOnly{rows: rows}, nil)
	ctx, f := context.Background(), analytics.Filter{Since: time.Now().Add(-time.Hour)}
	if allocs := testing.AllocsPerRun(5, func() { _, _ = agg.SessionTurns(ctx, f) }); allocs != 1 {
		t.Errorf("reading %d turns allocated %v times, want one slice", len(rows), allocs)
	}
}
