package observ

import (
	"testing"
	"time"

	"github.com/felixgeelhaar/tokenops/internal/domainevents"
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
