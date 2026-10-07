package sqlite

import (
	"context"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// One malformed envelope must not cost the batch its good rows: the bus
// would retry the whole batch and then drop it. The bad one is skipped
// and counted, the rest are stored.
func TestAppendBatchSkipsInvalidEnvelopes(t *testing.T) {
	base := time.Date(2026, 5, 9, 10, 0, 0, 0, time.UTC)
	good := func(id string) *eventschema.Envelope {
		return mustPromptEnvelope(t, id, base, &eventschema.PromptEvent{Provider: eventschema.ProviderOpenAI})
	}
	mismatched := &eventschema.Envelope{
		ID: "bad", SchemaVersion: eventschema.SchemaVersion, Type: eventschema.EventTypePrompt,
		Timestamp: base, Payload: &eventschema.WorkflowEvent{},
	}
	cases := []struct {
		name        string
		batch       []*eventschema.Envelope
		wantRows    int64
		wantSkipped int64
	}{
		{"all valid", []*eventschema.Envelope{good("a"), good("b")}, 2, 0},
		{"one invalid among valid", []*eventschema.Envelope{good("a"), mismatched, good("b")}, 2, 1},
		{"nil and invalid", []*eventschema.Envelope{nil, good("a"), mismatched}, 1, 2},
		{"all invalid", []*eventschema.Envelope{nil, mismatched}, 0, 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newTestStore(t)
			ctx := context.Background()
			if err := s.AppendBatch(ctx, tc.batch); err != nil {
				t.Fatalf("AppendBatch: %v", err)
			}
			n, err := s.Count(ctx, Filter{})
			if err != nil {
				t.Fatal(err)
			}
			if n != tc.wantRows {
				t.Errorf("rows = %d, want %d", n, tc.wantRows)
			}
			if got := s.SkippedInvalid(); got != tc.wantSkipped {
				t.Errorf("SkippedInvalid = %d, want %d", got, tc.wantSkipped)
			}
		})
	}
}
