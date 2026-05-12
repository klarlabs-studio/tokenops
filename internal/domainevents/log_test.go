package domainevents

import (
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestJSONLogAppendsAndReplays(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	log, err := NewJSONLog(path)
	if err != nil {
		t.Fatal(err)
	}
	bus := &Bus{}
	log.Attach(bus, nil)

	bus.Publish(WorkflowStarted{WorkflowID: "wf-1", At: time.Now().UTC()})
	bus.Publish(OptimizationApplied{OptimizerKind: "prompt_compress", TokensSaved: 42, At: time.Now().UTC()})
	bus.Publish(BudgetExceeded{BudgetID: "weekly", SpentUSD: 150, LimitUSD: 100, At: time.Now().UTC()})
	if err := log.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	var seen []string
	if err := Replay(path, func(r Record) error {
		seen = append(seen, r.Kind)
		return nil
	}); err != nil {
		t.Fatalf("replay: %v", err)
	}
	if len(seen) != 3 {
		t.Fatalf("seen = %d, want 3", len(seen))
	}
	if seen[0] != "workflow.started" || seen[2] != "budget.exceeded" {
		t.Errorf("order broken: %v", seen)
	}
}

func TestReplayIntoRehydratesBus(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	log, err := NewJSONLog(path)
	if err != nil {
		t.Fatal(err)
	}
	source := &Bus{}
	log.Attach(source, nil)
	source.Publish(WorkflowStarted{WorkflowID: "wf-1", At: time.Now().UTC()})
	source.Publish(WorkflowCompleted{WorkflowID: "wf-1", At: time.Now().UTC()})
	_ = log.Close()

	target := &Bus{}
	var hits atomic.Int64
	target.Subscribe("*", func(Event) { hits.Add(1) })
	if err := ReplayInto(path, target); err != nil {
		t.Fatal(err)
	}
	if hits.Load() != 2 {
		t.Errorf("replayed = %d, want 2", hits.Load())
	}
}

func TestJSONLogRotatesAtMaxBytes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	// Tiny cap forces rotation after the first publish.
	log, err := NewJSONLogWithRotation(path, 32, 2)
	if err != nil {
		t.Fatal(err)
	}
	bus := &Bus{}
	log.Attach(bus, nil)
	// Push enough events to trigger multiple rotations.
	for range 12 {
		bus.Publish(WorkflowStarted{WorkflowID: "wf-many-chars-to-overflow", At: time.Now().UTC()})
	}
	if err := log.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("active log missing: %v", err)
	}
	if _, err := os.Stat(path + ".1"); err != nil {
		t.Errorf("first backup missing: %v", err)
	}
}

func TestJSONLogRotationHonoursMaxBackups(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	log, err := NewJSONLogWithRotation(path, 32, 2)
	if err != nil {
		t.Fatal(err)
	}
	bus := &Bus{}
	log.Attach(bus, nil)
	for range 60 {
		bus.Publish(WorkflowStarted{WorkflowID: "wf-long-id-to-bloat-the-line", At: time.Now().UTC()})
	}
	if err := log.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path + ".1"); err != nil {
		t.Errorf("backup .1 missing: %v", err)
	}
	if _, err := os.Stat(path + ".2"); err != nil {
		t.Errorf("backup .2 missing: %v", err)
	}
	// .3 must NOT exist — maxBackups=2.
	if _, err := os.Stat(path + ".3"); err == nil {
		t.Errorf(".3 exists despite maxBackups=2")
	}
}

func TestReplayLenientSkipsMalformed(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "events.jsonl")
	content := `{"kind":"workflow.started","at":"2026-05-11T12:00:00Z","payload":null}
THIS IS NOT JSON
{"kind":"budget.exceeded","at":"2026-05-11T12:00:01Z","payload":null}
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	var seen []string
	skipped, err := ReplayLenient(path, func(r Record) error {
		seen = append(seen, r.Kind)
		return nil
	})
	if err != nil {
		t.Fatalf("lenient replay: %v", err)
	}
	if skipped != 1 {
		t.Errorf("skipped = %d, want 1", skipped)
	}
	if len(seen) != 2 {
		t.Errorf("decoded records = %d, want 2 (skip the bad line)", len(seen))
	}
}

func TestReplayBeforeAttachDoesNotDoubleAppend(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	log, err := NewJSONLog(path)
	if err != nil {
		t.Fatal(err)
	}
	bus := &Bus{}
	log.Attach(bus, nil)
	bus.Publish(WorkflowStarted{WorkflowID: "wf-1", At: time.Now().UTC()})
	bus.Publish(BudgetExceeded{BudgetID: "weekly", At: time.Now().UTC()})
	if err := log.Close(); err != nil {
		t.Fatal(err)
	}

	// Boot simulation: new bus, replay-into BEFORE re-attaching.
	bus2 := &Bus{}
	var counted atomic.Int64
	bus2.Subscribe("*", func(Event) { counted.Add(1) })
	if err := ReplayInto(path, bus2); err != nil {
		t.Fatal(err)
	}
	if counted.Load() != 2 {
		t.Fatalf("counted = %d, want 2 (replay)", counted.Load())
	}
	// Now attach a fresh log; only NEW events should land.
	log2, err := NewJSONLog(path)
	if err != nil {
		t.Fatal(err)
	}
	log2.Attach(bus2, nil)
	bus2.Publish(WorkflowCompleted{WorkflowID: "wf-1", At: time.Now().UTC()})
	if err := log2.Close(); err != nil {
		t.Fatal(err)
	}
	// File now contains original 2 + new 1 = 3 lines.
	data, _ := os.ReadFile(path)
	lines := 0
	for _, b := range data {
		if b == '\n' {
			lines++
		}
	}
	if lines != 3 {
		t.Errorf("file lines = %d, want 3 (replay must not re-append)", lines)
	}
}

func TestReplayMissingPathOK(t *testing.T) {
	if err := Replay("/does/not/exist", func(Record) error { return nil }); err != nil {
		t.Errorf("missing path should be ok: %v", err)
	}
}
