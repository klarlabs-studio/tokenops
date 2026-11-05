package proxy

import (
	"context"
	"errors"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/headroom"
)

// stubGlance counts computations and, when gate is set, holds each one
// until it is released.
type stubGlance struct {
	calls atomic.Int32
	gate  chan struct{}
	err   error
}

func (s *stubGlance) compute(ctx context.Context, now time.Time) (headroom.Glance, error) {
	n := s.calls.Add(1)
	if s.gate != nil {
		select {
		case <-s.gate:
		case <-ctx.Done():
			return headroom.Glance{}, ctx.Err()
		}
	}
	if s.err != nil {
		return headroom.Glance{}, s.err
	}
	return headroom.Glance{Accounts: map[string]string{"n": strconv.Itoa(int(n))}}, nil
}

func generation(g headroom.Glance) string { return g.Accounts["n"] }

// The first glance waits for its computation; one inside the fresh period
// is the same answer, computed once.
func TestGlanceSnapshotComputesOnceWhileFresh(t *testing.T) {
	st := &stubGlance{}
	c := newGlanceSnapshots(st.compute, time.Minute)
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	for range 3 {
		g, err := c.get(context.Background(), now)
		if err != nil || generation(g) != "1" {
			t.Fatalf("got %q, %v", generation(g), err)
		}
	}
	if st.calls.Load() != 1 {
		t.Fatalf("computed %d times, want once", st.calls.Load())
	}
}

// A stale snapshot is answered at once while a newer one is computed in
// the background, so a slow computation never holds a request; the next
// request after it lands gets the new one.
func TestGlanceSnapshotServesStaleWhileRefreshing(t *testing.T) {
	st := &stubGlance{}
	c := newGlanceSnapshots(st.compute, time.Minute)
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	if _, err := c.get(context.Background(), now); err != nil {
		t.Fatal(err)
	}

	st.gate = make(chan struct{})
	later := now.Add(2 * time.Minute)
	start := time.Now()
	g, err := c.get(context.Background(), later)
	if err != nil || generation(g) != "1" {
		t.Fatalf("stale read: got %q, %v", generation(g), err)
	}
	if time.Since(start) > time.Second {
		t.Fatal("a stale read waited for the computation")
	}
	// A second stale read starts no second computation.
	if _, err := c.get(context.Background(), later); err != nil {
		t.Fatal(err)
	}
	close(st.gate)
	c.waitIdle()
	if st.calls.Load() != 2 {
		t.Fatalf("computed %d times, want 2", st.calls.Load())
	}
	if g, _ := c.get(context.Background(), later); generation(g) != "2" {
		t.Fatalf("after the refresh: got %q, want 2", generation(g))
	}
}

// With nothing to serve, a failed computation is the request's error; a
// later failure keeps the last good snapshot.
func TestGlanceSnapshotErrors(t *testing.T) {
	boom := errors.New("boom")
	st := &stubGlance{err: boom}
	c := newGlanceSnapshots(st.compute, time.Minute)
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	if _, err := c.get(context.Background(), now); !errors.Is(err, boom) {
		t.Fatalf("err = %v, want boom", err)
	}
	st.err = nil
	if g, err := c.get(context.Background(), now); err != nil || generation(g) != "2" {
		t.Fatalf("retry: got %q, %v", generation(g), err)
	}
	st.err = boom
	g, err := c.get(context.Background(), now.Add(2*time.Minute))
	c.waitIdle()
	if err != nil || generation(g) != "2" {
		t.Fatalf("failed refresh: got %q, %v; want the last good snapshot", generation(g), err)
	}
}

// A request that gives up while the first computation runs returns at
// once; the computation goes on and serves the next request.
func TestGlanceSnapshotFirstWaitHonoursTheRequest(t *testing.T) {
	st := &stubGlance{gate: make(chan struct{})}
	c := newGlanceSnapshots(st.compute, time.Minute)
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := c.get(ctx, now); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want the request's deadline", err)
	}
	close(st.gate)
	c.waitIdle()
	if g, err := c.get(context.Background(), now); err != nil || generation(g) != "1" {
		t.Fatalf("got %q, %v; want the computation the first request started", generation(g), err)
	}
	if st.calls.Load() != 1 {
		t.Fatalf("computed %d times, want once", st.calls.Load())
	}
}
