package domainevents

import (
	"sync/atomic"
	"testing"
	"time"
)

func TestBusDispatchesByKind(t *testing.T) {
	var b Bus
	var workflowStarts atomic.Int64
	var any atomic.Int64
	b.Subscribe(KindWorkflowStarted, func(Event) { workflowStarts.Add(1) })
	b.Subscribe("*", func(Event) { any.Add(1) })

	b.Publish(WorkflowStarted{WorkflowID: "wf-1", At: time.Now()})
	b.Publish(WorkflowCompleted{WorkflowID: "wf-1", At: time.Now()})

	if workflowStarts.Load() != 1 {
		t.Errorf("workflow.started subs = %d, want 1", workflowStarts.Load())
	}
	if any.Load() != 2 {
		t.Errorf("wildcard subs = %d, want 2", any.Load())
	}
}

func TestBusIgnoresNilEvent(t *testing.T) {
	var b Bus
	b.Subscribe("*", func(Event) { t.Fatal("nil event should not dispatch") })
	b.Publish(nil)
}

func TestBusConcurrentPublishSafe(t *testing.T) {
	var b Bus
	var hits atomic.Int64
	b.Subscribe(KindOptimizationApplied, func(Event) { hits.Add(1) })
	done := make(chan struct{})
	for range 4 {
		go func() {
			for range 50 {
				b.Publish(OptimizationApplied{OptimizerKind: "prompt_compress", At: time.Now()})
			}
			done <- struct{}{}
		}()
	}
	for range 4 {
		<-done
	}
	if hits.Load() != 200 {
		t.Errorf("hits = %d, want 200", hits.Load())
	}
}
