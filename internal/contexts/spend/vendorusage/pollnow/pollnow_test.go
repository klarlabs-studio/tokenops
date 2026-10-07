package pollnow

import (
	"context"
	"testing"
	"time"
)

func fired(c <-chan time.Time) bool {
	select {
	case <-c:
		return true
	case <-time.After(time.Second):
		return false
	}
}

// A refresh makes every running poller's ticker fire now, not at its
// next interval.
func TestFireTicksEveryRunningPoller(t *testing.T) {
	s := New(0)
	ctx := With(context.Background(), s)
	a, b := NewTicker(ctx, time.Hour), NewTicker(ctx, time.Hour)
	defer a.Stop()
	defer b.Stop()
	if n, ok := s.Fire(time.Now()); !ok || n != 2 {
		t.Fatalf("Fire = %d, %v; want both pollers", n, ok)
	}
	if !fired(a.C) || !fired(b.C) {
		t.Error("a ticker did not fire on refresh")
	}
}

// A refresh soon after the last one is refused: each click would
// otherwise poll every vendor again, into rate limits and bot checks.
func TestFireIsRateLimited(t *testing.T) {
	s := New(30 * time.Second)
	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	if _, ok := s.Fire(now); !ok {
		t.Fatal("the first refresh was refused")
	}
	if _, ok := s.Fire(now.Add(10 * time.Second)); ok {
		t.Error("a refresh 10s after the last was accepted")
	}
	if next := s.Next(); !next.Equal(now.Add(30 * time.Second)) {
		t.Errorf("next refresh at %v", next)
	}
	if _, ok := s.Fire(now.Add(31 * time.Second)); !ok {
		t.Error("a refresh after the limit was refused")
	}
}

// Without a signal in the context the ticker is the plain interval
// ticker, and a stopped ticker no longer counts as a poller.
func TestTickerWithoutSignalAndAfterStop(t *testing.T) {
	plain := NewTicker(context.Background(), 10*time.Millisecond)
	if !fired(plain.C) {
		t.Error("the interval did not tick")
	}
	plain.Stop()

	s := New(0)
	tk := NewTicker(With(context.Background(), s), time.Hour)
	tk.Stop()
	if n, _ := s.Fire(time.Now()); n != 0 {
		t.Errorf("a stopped ticker was signalled (%d)", n)
	}
}
