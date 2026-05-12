package domainevents

import (
	"testing"
	"time"
)

func TestBusTracksDispatchAndDrop(t *testing.T) {
	var b Bus
	b.Subscribe("*", func(Event) {})
	b.Publish(WorkflowStarted{WorkflowID: "wf-1", At: time.Now()})
	b.Publish(nil)
	b.Publish(WorkflowCompleted{WorkflowID: "wf-1", At: time.Now()})
	if b.DispatchedCount() != 2 {
		t.Errorf("dispatched = %d, want 2", b.DispatchedCount())
	}
	if b.DroppedCount() != 1 {
		t.Errorf("dropped = %d, want 1", b.DroppedCount())
	}
}

func TestBusAsyncDropsOnFullQueue(t *testing.T) {
	b := &Bus{}
	b.StartAsync(1)
	block := make(chan struct{})
	b.Subscribe("*", func(Event) { <-block })
	for range 100 {
		b.Publish(WorkflowStarted{WorkflowID: "x", At: time.Now()})
	}
	if b.DroppedCount() == 0 {
		close(block)
		b.Close()
		t.Fatal("expected drops under saturation")
	}
	close(block)
	b.Close()
}

func TestBusRecoversFromHandlerPanic(t *testing.T) {
	b := &Bus{}
	survived := 0
	b.Subscribe("*", func(Event) { panic("boom") })
	b.Subscribe("*", func(Event) { survived++ })
	b.Publish(WorkflowStarted{WorkflowID: "x", At: time.Now()})
	if b.PanicCount() != 1 {
		t.Errorf("PanicCount = %d, want 1", b.PanicCount())
	}
	if survived != 1 {
		t.Errorf("subsequent handler did not run after panic (survived=%d)", survived)
	}
}

func TestSubscriptionCancelStopsDelivery(t *testing.T) {
	b := &Bus{}
	got := 0
	sub := b.Subscribe("*", func(Event) { got++ })
	b.Publish(WorkflowStarted{WorkflowID: "x", At: time.Now()})
	sub.Cancel()
	b.Publish(WorkflowStarted{WorkflowID: "y", At: time.Now()})
	if got != 1 {
		t.Errorf("got = %d, want 1 (second publish should not fire)", got)
	}
	// Idempotent.
	sub.Cancel()
}

func TestBusFlagsSlowHandlers(t *testing.T) {
	b := &Bus{SlowHandlerThreshold: time.Millisecond}
	b.Subscribe("*", func(Event) { time.Sleep(5 * time.Millisecond) })
	b.Publish(WorkflowStarted{WorkflowID: "x", At: time.Now()})
	if b.SlowDispatchCount() != 1 {
		t.Errorf("slow = %d, want 1", b.SlowDispatchCount())
	}
}
