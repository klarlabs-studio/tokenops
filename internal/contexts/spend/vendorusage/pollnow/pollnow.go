// Package pollnow lets a caller ask the running vendor-usage pollers to
// poll now rather than at their next interval: the menu bar's Refresh,
// for one. A plan window read every 15 minutes can be 15 minutes old, and
// re-reading the daemon only returns the same reading again.
//
// A poller takes its ticker from NewTicker instead of time.NewTicker; the
// ticker fires on the interval and whenever the Signal in its context
// fires.
package pollnow

import (
	"context"
	"sync"
	"time"
)

// Signal tells the pollers subscribed to it to poll now. It refuses a
// refresh within minGap of the last one: each would poll every vendor
// again, into their rate limits and bot checks.
type Signal struct {
	minGap time.Duration

	mu   sync.Mutex
	last time.Time
	subs map[chan struct{}]struct{}
}

// New returns a Signal that accepts one refresh per minGap.
func New(minGap time.Duration) *Signal {
	return &Signal{minGap: minGap, subs: map[chan struct{}]struct{}{}}
}

// Fire asks every subscribed poller to poll now, at now. It reports how
// many it asked, and false when the last refresh was too recent.
func (s *Signal) Fire(now time.Time) (int, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.last.IsZero() && now.Sub(s.last) < s.minGap {
		return 0, false
	}
	s.last = now
	for c := range s.subs {
		select {
		case c <- struct{}{}:
		default: // a refresh is already pending for this poller
		}
	}
	return len(s.subs), true
}

// Refresh fires at now and reports, with how many it asked and whether it
// was accepted, when the next refresh will be.
func (s *Signal) Refresh(now time.Time) (int, bool, time.Time) {
	n, ok := s.Fire(now)
	return n, ok, s.Next()
}

// Next is when the next refresh will be accepted.
func (s *Signal) Next() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.last.IsZero() {
		return time.Time{}
	}
	return s.last.Add(s.minGap)
}

func (s *Signal) subscribe() (chan struct{}, func()) {
	c := make(chan struct{}, 1)
	s.mu.Lock()
	s.subs[c] = struct{}{}
	s.mu.Unlock()
	return c, func() {
		s.mu.Lock()
		delete(s.subs, c)
		s.mu.Unlock()
	}
}

type ctxKey struct{}

// With returns ctx carrying s, for the pollers started under it.
func With(ctx context.Context, s *Signal) context.Context {
	return context.WithValue(ctx, ctxKey{}, s)
}

// Ticker fires on its interval and on a refresh. C has the same shape as
// time.Ticker's, so a poller's loop does not change.
type Ticker struct {
	C    <-chan time.Time
	stop func()
}

// NewTicker ticks every d, and whenever the Signal in ctx fires. Without
// a Signal it is a plain interval ticker.
func NewTicker(ctx context.Context, d time.Duration) *Ticker {
	t := time.NewTicker(d)
	s, _ := ctx.Value(ctxKey{}).(*Signal)
	if s == nil {
		return &Ticker{C: t.C, stop: t.Stop}
	}
	refresh, unsubscribe := s.subscribe()
	out := make(chan time.Time, 1)
	done := make(chan struct{})
	send := func(at time.Time) {
		select {
		case out <- at:
		default: // a tick is already pending
		}
	}
	go func() {
		for {
			select {
			case <-done:
				return
			case at := <-t.C:
				send(at)
			case <-refresh:
				send(time.Now())
			}
		}
	}()
	var once sync.Once
	return &Ticker{C: out, stop: func() {
		once.Do(func() {
			t.Stop()
			unsubscribe()
			close(done)
		})
	}}
}

// Stop stops the ticker and leaves the Signal.
func (t *Ticker) Stop() { t.stop() }
