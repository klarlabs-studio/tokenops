package observ

import (
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/domainevents"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

func TestEventCounterCountsByKind(t *testing.T) {
	c := NewEventCounter()
	bus := &domainevents.Bus{}
	c.Subscribe(bus)

	bus.Publish(domainevents.WorkflowStarted{WorkflowID: "wf-1", At: time.Now()})
	bus.Publish(domainevents.WorkflowStarted{WorkflowID: "wf-2", At: time.Now()})
	bus.Publish(domainevents.WorkflowCompleted{WorkflowID: "wf-1", At: time.Now()})
	bus.Publish(domainevents.OptimizationApplied{OptimizerKind: "prompt_compress", At: time.Now()})

	counts := c.Counts()
	if counts["workflow.started"] != 2 {
		t.Errorf("workflow.started = %d, want 2", counts["workflow.started"])
	}
	if counts["workflow.completed"] != 1 {
		t.Errorf("workflow.completed = %d", counts["workflow.completed"])
	}
	if counts["optimization.applied"] != 1 {
		t.Errorf("optimization.applied = %d", counts["optimization.applied"])
	}
	if c.Total() != 4 {
		t.Errorf("total = %d, want 4", c.Total())
	}
	kinds := c.Kinds()
	if len(kinds) != 3 {
		t.Errorf("kinds = %v, want 3 entries", kinds)
	}
	// kinds returned sorted
	if kinds[0] >= kinds[1] || kinds[1] >= kinds[2] {
		t.Errorf("kinds not sorted: %v", kinds)
	}
}

func TestEventCounterNilBusSafe(t *testing.T) {
	c := NewEventCounter()
	c.Subscribe(nil) // must not panic
	if c.Total() != 0 {
		t.Errorf("expected 0 total")
	}
}

func TestEventCounterHydratesCanonicalDomainEnvelopes(t *testing.T) {
	c := NewEventCounter()
	first := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	last := first.Add(time.Hour)
	c.Hydrate([]*eventschema.Envelope{
		{Timestamp: last, Type: eventschema.EventTypeDomain, Payload: &eventschema.DomainEvent{Kind: "budget.exceeded"}},
		{Timestamp: first, Type: eventschema.EventTypeDomain, Payload: &eventschema.DomainEvent{Kind: "budget.exceeded"}},
		{Timestamp: last, Type: eventschema.EventTypePrompt, Payload: &eventschema.PromptEvent{}},
	})
	if c.Total() != 2 || c.Counts()["budget.exceeded"] != 2 {
		t.Fatalf("hydrated counts = %+v", c.Counts())
	}
	span := c.Spans()["budget.exceeded"]
	if !span.First.Equal(first) || !span.Last.Equal(last) {
		t.Fatalf("hydrated span = %+v", span)
	}
}

// The daemon hydrates the counter from its persisted log at boot, so a
// count is a lifetime total. The span says when the counted events
// happened: replayed ones at their original time, live ones on arrival —
// so 274 alerts from June do not read as 274 now.
func TestEventCounterRecordsWhenEachKindHappened(t *testing.T) {
	c := NewEventCounter()
	now := time.Date(2026, 9, 19, 20, 0, 0, 0, time.UTC)
	c.now = func() time.Time { return now }
	bus := &domainevents.Bus{}
	c.Subscribe(bus)

	june := time.Date(2026, 6, 14, 23, 49, 0, 0, time.UTC)
	bus.Publish(domainevents.NewReplayed("budget.exceeded", june))
	bus.Publish(domainevents.NewReplayed("budget.exceeded", june.Add(time.Hour)))
	bus.Publish(domainevents.WorkflowStarted{WorkflowID: "wf", At: now})

	spans := c.Spans()
	if b := spans["budget.exceeded"]; !b.First.Equal(june) || !b.Last.Equal(june.Add(time.Hour)) {
		t.Errorf("budget.exceeded span = %+v, want June, from the replayed times", b)
	}
	if w := spans["workflow.started"]; !w.Last.Equal(now) {
		t.Errorf("live event span = %+v, want now", w)
	}
}
