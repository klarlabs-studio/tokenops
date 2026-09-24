package workflow

import (
	"context"
	"strconv"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/spend/spend"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

type envelopeCapture struct{ events []*eventschema.Envelope }

func (c *envelopeCapture) Publish(env *eventschema.Envelope) { c.events = append(c.events, env) }

func TestReconstructPublishesWorkflowEvents(t *testing.T) {
	store := newStore(t)
	now := time.Now().UTC()
	for i := range 3 {
		env := mkStep(
			"e"+strconv.Itoa(i), "wf-1", "agent-x", "gpt-4",
			now.Add(time.Duration(i)*time.Second), 100, 20, 0.001, 200*time.Millisecond,
		)
		if err := store.Append(context.Background(), env); err != nil {
			t.Fatalf("append: %v", err)
		}
	}
	bus := &envelopeCapture{}
	SetEventBus(bus)
	t.Cleanup(func() { SetEventBus(nil) })

	if _, err := Reconstruct(context.Background(), store, spend.NewEngine(spend.DefaultTable()), "wf-1"); err != nil {
		t.Fatalf("reconstruct: %v", err)
	}
	if len(bus.events) != 1 {
		t.Fatalf("workflow events = %d, want 1", len(bus.events))
	}
	if got := bus.events[0].Payload.(*eventschema.DomainEvent).Kind; got != "workflow.observed" {
		t.Errorf("event kind = %q, want workflow.observed", got)
	}
}
