package analytics

import (
	"context"
	"math"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/spend/spend"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// A session turn's API-equivalent value prices its cache writes at the
// write rate, whether the event carries them in the payload or, written
// before that field existed, as Claude Code's cache_creation_input
// attribute.
func TestSessionTurnsPriceCacheWrites(t *testing.T) {
	turn := func(id string, payloadWrites int64, attrs map[string]string) *eventschema.Envelope {
		return &eventschema.Envelope{
			ID: id, SchemaVersion: eventschema.SchemaVersion, Type: eventschema.EventTypePrompt,
			Timestamp: time.Now().UTC().Add(-time.Hour), Attributes: attrs,
			Payload: &eventschema.PromptEvent{
				Provider: eventschema.ProviderAnthropic, RequestModel: "claude-sonnet-5", SessionID: "s-" + id,
				InputTokens: 1_000_000, TotalTokens: 1_000_000, CacheWriteInputTokens: payloadWrites,
				CostSource: eventschema.CostSourcePlanIncluded,
			},
		}
	}
	st := storeWith(t,
		turn("payload", 1_000_000, nil),
		turn("legacy", 0, map[string]string{"cache_creation_input": "1000000"}),
	)
	turns, err := New(st, spend.NewEngine(spend.DefaultTable())).SessionTurns(context.Background(),
		Filter{Since: time.Now().Add(-24 * time.Hour)})
	if err != nil {
		t.Fatalf("session turns: %v", err)
	}
	if len(turns) != 2 {
		t.Fatalf("got %d turns, want 2", len(turns))
	}
	for _, tn := range turns {
		// claude-sonnet-5 writes are $2.50 per million against $2 input.
		if !tn.Priced || math.Abs(tn.APIEquivalentUSD-2.5) > 1e-9 {
			t.Errorf("%s: value = %v (priced %v), want 2.5", tn.SessionID, tn.APIEquivalentUSD, tn.Priced)
		}
	}
}
