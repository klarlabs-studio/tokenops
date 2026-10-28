package daemon

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/state"
)

func countingAssess(n *atomic.Int32, delay time.Duration) func() []state.SourceReport {
	return func() []state.SourceReport {
		n.Add(1)
		time.Sleep(delay)
		return []state.SourceReport{{Tag: "t"}}
	}
}

// A snapshot younger than its age limit is served as it is: the route
// answers at once rather than redoing every store query and folder walk.
func TestSourceSnapshotServesAFreshSnapshot(t *testing.T) {
	var n atomic.Int32
	now := time.Date(2026, 10, 28, 1, 0, 0, 0, time.UTC)
	s := newSourceSnapshot(countingAssess(&n, 0), time.Minute)
	s.now = func() time.Time { return now }

	if got := s.Reports(); len(got) != 1 || n.Load() != 1 {
		t.Fatalf("first read: %d reports, %d assessments", len(got), n.Load())
	}
	now = now.Add(59 * time.Second)
	s.Reports()
	if n.Load() != 1 {
		t.Errorf("a fresh snapshot was re-assessed (%d)", n.Load())
	}
	// Past the limit the answer is assessed again, not served stale: an
	// old status answer is the failure this check exists to catch.
	now = now.Add(2 * time.Second)
	s.Reports()
	if n.Load() != 2 {
		t.Errorf("a stale snapshot was served (%d assessments)", n.Load())
	}
}

// Readers that arrive while an assessment runs wait for it rather than
// starting their own, so a burst of status calls on a loaded machine
// costs one assessment, not one each.
func TestSourceSnapshotAssessesOnceForConcurrentReaders(t *testing.T) {
	var n atomic.Int32
	s := newSourceSnapshot(countingAssess(&n, 50*time.Millisecond), time.Minute)
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if len(s.Reports()) != 1 {
				t.Error("a reader got no reports")
			}
		}()
	}
	wg.Wait()
	if n.Load() != 1 {
		t.Errorf("%d assessments for one burst, want 1", n.Load())
	}
}

// Refresh replaces the snapshot, which is what the background loop calls.
func TestSourceSnapshotRefresh(t *testing.T) {
	var n atomic.Int32
	s := newSourceSnapshot(countingAssess(&n, 0), time.Minute)
	s.Refresh()
	s.Reports()
	if n.Load() != 1 {
		t.Errorf("reads after a refresh re-assessed (%d)", n.Load())
	}
}
