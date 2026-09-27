package sqlite

import (
	"context"
	"slices"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/pkg/eventschema"
)

func TestEachEventIDStreamsEveryStoredID(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	for _, id := range []string{"b", "a", "c"} {
		env := mustPromptEnvelope(t, id, time.Now(), &eventschema.PromptEvent{Provider: eventschema.ProviderAnthropic})
		if err := s.Append(ctx, env); err != nil {
			t.Fatalf("append %s: %v", id, err)
		}
	}
	var got []string
	if err := s.EachEventID(ctx, func(id string) { got = append(got, id) }); err != nil {
		t.Fatalf("EachEventID: %v", err)
	}
	slices.Sort(got)
	if want := []string{"a", "b", "c"}; !slices.Equal(got, want) {
		t.Fatalf("ids = %v, want %v", got, want)
	}
}
