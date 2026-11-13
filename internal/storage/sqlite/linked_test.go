package sqlite

import (
	"context"
	"sort"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// Linked keeps exactly the events that can join work: linked to an execution
// or an experiment, or whose actor is one of the named actors. The association
// columns decide it, so the answer does not depend on the payload.
func TestEachMatchingLinkedKeepsOnlyEventsThatJoinWork(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	at := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	ev := func(id string, assoc eventschema.Association, corr eventschema.Correlation) *eventschema.Envelope {
		return &eventschema.Envelope{ID: id, SchemaVersion: eventschema.SchemaVersion,
			Type: eventschema.EventTypePrompt, Timestamp: at, Source: "test",
			Association: assoc, Correlation: corr,
			Payload: &eventschema.PromptEvent{Provider: eventschema.ProviderAnthropic}}
	}
	envs := []*eventschema.Envelope{
		ev("execution", eventschema.Association{Execution: "e1"}, eventschema.Correlation{}),
		ev("experiment", eventschema.Association{}, eventschema.Correlation{Experiment: "x1"}),
		ev("named-actor", eventschema.Association{Actor: "session:a"}, eventschema.Correlation{}),
		ev("other-actor", eventschema.Association{Actor: "session:b"}, eventschema.Correlation{}),
		ev("bare", eventschema.Association{}, eventschema.Correlation{}),
	}
	if err := s.AppendBatch(ctx, envs); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		f    Filter
		want []string
	}{
		{"linked without actors", Filter{Linked: true}, []string{"execution", "experiment"}},
		{"linked with a named actor", Filter{Linked: true, Actors: []string{"session:a"}}, []string{"execution", "experiment", "named-actor"}},
		{"linked with no matching actor", Filter{Linked: true, Actors: []string{"session:z"}}, []string{"execution", "experiment"}},
		{"not linked is unchanged", Filter{}, []string{"execution", "experiment", "named-actor", "other-actor", "bare"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var got []string
			if err := s.EachMatching(ctx, tc.f, func(e *eventschema.Envelope) error {
				got = append(got, e.ID)
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			sort.Strings(got)
			want := append([]string(nil), tc.want...)
			sort.Strings(want)
			if len(got) != len(want) {
				t.Fatalf("got %v, want %v", got, want)
			}
			for i := range got {
				if got[i] != want[i] {
					t.Fatalf("got %v, want %v", got, want)
				}
			}
		})
	}
}
