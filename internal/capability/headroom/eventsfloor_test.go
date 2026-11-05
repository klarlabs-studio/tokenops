package headroom_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/headroom"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// sinceReader records how far back each read of a computation reaches.
type sinceReader struct {
	mu       sync.Mutex
	earliest time.Time
}

func (r *sinceReader) ReadEvents(_ context.Context, _ eventschema.EventType, since time.Time) ([]*eventschema.Envelope, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.earliest.IsZero() || since.Before(r.earliest) {
		r.earliest = since
	}
	return nil, nil
}

func (r *sinceReader) CountBySource(context.Context, time.Time, time.Time) (map[string]int64, error) {
	return map[string]int64{}, nil
}

// The daemon warms its event cache from EventsFloor so a glance answers
// from memory. A read reaching further back fills the cache from the store
// inside the glance, holding the cache while it does: in a store a month of
// heavy Codex use deep, every glance then waited minutes behind the plan
// advice's 30-day replay, and the menu bar showed nothing.
func TestGlanceReadsNothingOlderThanEventsFloor(t *testing.T) {
	for _, now := range []time.Time{
		time.Date(2026, 11, 5, 11, 0, 0, 0, time.UTC),  // early in the month
		time.Date(2026, 11, 28, 11, 0, 0, 0, time.UTC), // late in the month
	} {
		r := &sinceReader{}
		deps := headroom.Deps{
			Config:      cfgWith(map[string]string{"anthropic": "claude-max", "openai": "gpt-pro"}),
			Reader:      r,
			AdviceCache: headroom.NewAdviceCache(),
		}
		if _, err := headroom.ComputeGlance(context.Background(), deps, now); err != nil {
			t.Fatal(err)
		}
		if floor := headroom.EventsFloor(now); r.earliest.Before(floor) {
			t.Errorf("at %s a glance read from %s, before EventsFloor %s", now.Format(time.DateOnly), r.earliest.Format(time.DateTime), floor.Format(time.DateTime))
		}
	}
}
