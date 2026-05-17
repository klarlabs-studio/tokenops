package tasks

import (
	"path/filepath"
	"testing"
	"time"
)

// Start + Done + List round-trips: start two tasks, close one,
// confirm the open/closed states + ordering.
func TestStartDoneList(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tasks.jsonl")
	base := time.Date(2026, 5, 17, 10, 0, 0, 0, time.UTC)
	clock := func() time.Time { return base }
	t1, err := Start(path, "fix auth", "", clock)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if t1.IsOpen() != true {
		t.Errorf("new task should be open")
	}

	clock = func() time.Time { return base.Add(time.Minute) }
	t2, err := Start(path, "write tests", "sess-abc", clock)
	if err != nil {
		t.Fatalf("Start 2: %v", err)
	}

	// Done closes the most recent open task.
	clock = func() time.Time { return base.Add(2 * time.Minute) }
	closed, err := Done(path, clock)
	if err != nil {
		t.Fatalf("Done: %v", err)
	}
	if closed.ID != t2.ID {
		t.Errorf("expected most recent (%s) closed; got %s", t2.ID, closed.ID)
	}

	all, err := List(path)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("got %d tasks; want 2", len(all))
	}
	if !all[0].IsOpen() {
		t.Errorf("task 1 should still be open")
	}
	if all[1].IsOpen() {
		t.Errorf("task 2 should be closed")
	}
	if all[1].SessionID != "sess-abc" {
		t.Errorf("session_id lost: %q", all[1].SessionID)
	}
	if all[1].Duration(clock) != time.Minute {
		t.Errorf("duration = %v; want 1m", all[1].Duration(clock))
	}
	_ = t1 // suppress unused — t1 used implicitly through List
}

// Done with no open task returns a clear error.
func TestDoneNoOpenTask(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tasks.jsonl")
	_, err := Done(path, time.Now)
	if err == nil {
		t.Fatal("expected error when no open task")
	}
}

// Start with empty description rejects.
func TestStartRequiresDescription(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tasks.jsonl")
	_, err := Start(path, "", "", time.Now)
	if err == nil {
		t.Error("expected error for empty description")
	}
}

// List on a missing file returns empty slice + nil error.
func TestListMissingFile(t *testing.T) {
	all, err := List(filepath.Join(t.TempDir(), "nonexistent.jsonl"))
	if err != nil {
		t.Errorf("List on missing file should not error: %v", err)
	}
	if len(all) != 0 {
		t.Errorf("got %d tasks; want 0", len(all))
	}
}
