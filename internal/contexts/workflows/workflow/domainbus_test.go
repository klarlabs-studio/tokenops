package workflow

import (
	"context"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/felixgeelhaar/tokenops/internal/contexts/spend/spend"
	"github.com/felixgeelhaar/tokenops/internal/domainevents"
	"github.com/felixgeelhaar/tokenops/pkg/eventschema"
)

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
	bus := &domainevents.Bus{}
	var observed atomic.Int64
	bus.Subscribe(domainevents.KindWorkflowObserved, func(domainevents.Event) { observed.Add(1) })
	SetDomainBus(bus)
	t.Cleanup(func() { SetDomainBus(nil) })

	if _, err := Reconstruct(context.Background(), store, spend.NewEngine(spend.DefaultTable()), "wf-1"); err != nil {
		t.Fatalf("reconstruct: %v", err)
	}
	if observed.Load() != 1 {
		t.Errorf("workflow.observed events = %d, want 1", observed.Load())
	}
}

var _ = eventschema.EventTypePrompt // keep import grouped with sibling test
