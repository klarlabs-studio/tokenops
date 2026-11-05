package proxy

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/findings"
)

// counter is a computation that returns how many times it ran and, when
// gate is set, holds each run until it is released.
type counter struct {
	calls atomic.Int32
	gate  chan struct{}
	err   error
}

func (c *counter) compute(ctx context.Context, _ time.Time) (int, error) {
	n := int(c.calls.Add(1))
	if c.gate != nil {
		select {
		case <-c.gate:
		case <-ctx.Done():
			return 0, ctx.Err()
		}
	}
	if c.err != nil {
		return 0, c.err
	}
	return n, nil
}

var snapNow = time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)

func newCounterSnapshots(c *counter, path string) *snapshots[int] {
	return &snapshots[int]{compute: c.compute, fresh: time.Minute, path: path, maxDiskAge: 24 * time.Hour}
}

// The first answer waits for its computation; one inside the fresh period
// is the same answer, computed once.
func TestSnapshotsComputeOnceWhileFresh(t *testing.T) {
	c := &counter{}
	s := newCounterSnapshots(c, "")
	for range 3 {
		v, at, err := s.get(context.Background(), snapNow)
		if err != nil || v != 1 || !at.Equal(snapNow) {
			t.Fatalf("got %d at %s, %v", v, at, err)
		}
	}
	if c.calls.Load() != 1 {
		t.Fatalf("computed %d times, want once", c.calls.Load())
	}
}

// A stale result is answered at once while a newer one is computed in the
// background, so a slow computation never holds a request; the next
// request after it lands gets the new one.
func TestSnapshotsServeStaleWhileRefreshing(t *testing.T) {
	c := &counter{}
	s := newCounterSnapshots(c, "")
	if _, _, err := s.get(context.Background(), snapNow); err != nil {
		t.Fatal(err)
	}
	c.gate = make(chan struct{})
	later := snapNow.Add(2 * time.Minute)
	start := time.Now()
	v, at, err := s.get(context.Background(), later)
	if err != nil || v != 1 || !at.Equal(snapNow) {
		t.Fatalf("stale read: got %d at %s, %v", v, at, err)
	}
	if time.Since(start) > time.Second {
		t.Fatal("a stale read waited for the computation")
	}
	if _, _, err := s.get(context.Background(), later); err != nil {
		t.Fatal(err)
	}
	close(c.gate)
	s.waitIdle()
	if c.calls.Load() != 2 {
		t.Fatalf("computed %d times, want 2: a second stale read started another", c.calls.Load())
	}
	if v, at, _ := s.get(context.Background(), later); v != 2 || !at.Equal(later) {
		t.Fatalf("after the refresh: got %d at %s, want 2 at %s", v, at, later)
	}
}

// With nothing to answer, a failed computation is the request's error; a
// later failure keeps the last good result.
func TestSnapshotsErrors(t *testing.T) {
	boom := errors.New("boom")
	c := &counter{err: boom}
	s := newCounterSnapshots(c, "")
	if _, _, err := s.get(context.Background(), snapNow); !errors.Is(err, boom) {
		t.Fatalf("err = %v, want boom", err)
	}
	c.err = nil
	if v, _, err := s.get(context.Background(), snapNow); err != nil || v != 2 {
		t.Fatalf("retry: got %d, %v", v, err)
	}
	c.err = boom
	v, _, err := s.get(context.Background(), snapNow.Add(2*time.Minute))
	s.waitIdle()
	if err != nil || v != 2 {
		t.Fatalf("failed refresh: got %d, %v; want the last good result", v, err)
	}
}

// A request that gives up while the first computation runs returns at
// once; the computation goes on and serves the next request.
func TestSnapshotsFirstWaitHonoursTheRequest(t *testing.T) {
	c := &counter{gate: make(chan struct{})}
	s := newCounterSnapshots(c, "")
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, _, err := s.get(ctx, snapNow); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want the request's deadline", err)
	}
	close(c.gate)
	s.waitIdle()
	if v, _, err := s.get(context.Background(), snapNow); err != nil || v != 1 {
		t.Fatalf("got %d, %v; want the computation the first request started", v, err)
	}
	if c.calls.Load() != 1 {
		t.Fatalf("computed %d times, want once", c.calls.Load())
	}
}

// After a restart the saved result is answered at once, with when it was
// computed, while the first computation runs; an old or unreadable save is
// not.
func TestSnapshotsAnswerTheSavedResultAfterAStart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache", "glance.json")
	first := newCounterSnapshots(&counter{}, path)
	if _, _, err := first.get(context.Background(), snapNow); err != nil {
		t.Fatal(err)
	}

	// The next start: its computation is held open.
	c := &counter{gate: make(chan struct{})}
	defer close(c.gate)
	restarted := newCounterSnapshots(c, path)
	later := snapNow.Add(10 * time.Minute)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	v, at, err := restarted.get(ctx, later)
	if err != nil || v != 1 || !at.Equal(snapNow) {
		t.Fatalf("after a start: got %d at %s, %v; want the saved 1 at %s", v, at, err, snapNow)
	}
	for deadline := time.Now().Add(5 * time.Second); c.calls.Load() != 1; {
		if time.Now().After(deadline) {
			t.Fatal("answering the saved result started no computation")
		}
		time.Sleep(time.Millisecond)
	}

	for _, tc := range []struct {
		name  string
		write func()
		key   string
		at    time.Time
	}{ // in order: the last one spoils the file
		{"old", func() {}, "", snapNow.Add(48 * time.Hour)},
		{"another config", func() {}, "other", later},
		{"unreadable", func() { _ = os.WriteFile(path, []byte("{"), 0o600) }, "", later},
	} {
		name := tc.name
		tc.write()
		s := newCounterSnapshots(&counter{gate: make(chan struct{})}, path)
		s.key = tc.key
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		_, _, err := s.get(ctx, tc.at)
		cancel()
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("%s save: err = %v, want to wait for a computation", name, err)
		}
	}
}

// stale makes the next request start a newer computation.
func TestSnapshotsStaleRecomputes(t *testing.T) {
	c := &counter{}
	s := newCounterSnapshots(c, "")
	if _, _, err := s.get(context.Background(), snapNow); err != nil {
		t.Fatal(err)
	}
	s.stale()
	if v, _, _ := s.get(context.Background(), snapNow); v != 1 {
		t.Fatalf("got %d; the stale result is answered while the next one computes", v)
	}
	s.waitIdle()
	if v, _, _ := s.get(context.Background(), snapNow); v != 2 {
		t.Fatalf("got %d, want the recomputed 2", v)
	}
}

// A finding acted on stays out of findings computed before the action,
// and comes back only if a computation after it still draws it.
func TestActedFindingLeavesTheSnapshot(t *testing.T) {
	s := &Server{}
	r := findings.Report{Findings: []findings.Finding{{Key: "a"}, {Key: "b"}}}
	computed := snapNow
	s.noteActed("a", snapNow.Add(time.Second))
	if got := s.withoutActed(r, computed); len(got.Findings) != 1 || got.Findings[0].Key != "b" {
		t.Fatalf("before a newer computation: %+v, want only b", got.Findings)
	}
	if got := s.withoutActed(r, snapNow.Add(time.Minute)); len(got.Findings) != 2 {
		t.Fatalf("a computation after the action decides: %+v, want both", got.Findings)
	}
	if len(s.acted) != 0 {
		t.Fatalf("an action older than the computation is still remembered: %v", s.acted)
	}
}
