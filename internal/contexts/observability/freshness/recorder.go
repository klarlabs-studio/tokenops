package freshness

import (
	"sync"
	"time"
)

// Recorder is what a reader reports its own health to.
//
// Every poller in TokenOps already tracked its last error — four
// packages each grew a private lastErr field and a LastError method, and
// not one of those methods had a call site. The information existed and
// reached nobody, which is the same shape as the outage it was supposed
// to prevent.
//
// None of them tracked the last *success*, and that is the fact that
// does the work: a poller running every minute and being refused
// produces exactly the same silence as a vendor nobody is using. Only
// "it last worked at noon and has been failing since 12:01" tells them
// apart.
//
// Safe for concurrent use: pollers write from their own goroutines while
// the status surfaces read from another.
type Recorder struct {
	mu   sync.Mutex
	poll Poll
}

// NewRecorder returns an empty recorder. Empty is not a claim of
// success — a reader that has never reported in reads as unknown, not as
// healthy.
func NewRecorder() *Recorder { return &Recorder{} }

// Succeeded records a completed poll and clears any previous error, so a
// reader that recovers stops reading as broken.
func (r *Recorder) Succeeded(at time.Time) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.poll.LastAttemptAt = at
	r.poll.LastSuccessAt = at
	r.poll.LastError = nil
	r.poll.LastErrorAt = time.Time{}
}

// Failed records an attempt that did not complete. The last success is
// deliberately left intact: how long a reader has been failing is the
// diagnosis, and overwriting it would throw that away.
func (r *Recorder) Failed(err error, at time.Time) {
	if r == nil || err == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.poll.LastAttemptAt = at
	r.poll.LastError = err
	r.poll.LastErrorAt = at
}

// Poll returns a snapshot of what this reader has reported.
func (r *Recorder) Poll() Poll {
	if r == nil {
		return Poll{}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.poll
}

// Registry is the one place that knows every reader's health, keyed by
// the source tag the event store records.
//
// It exists so the daemon can hand a single object to the status
// surfaces instead of each of them reaching into four poller structs
// that do not share an interface.
//
// The nil Registry is usable. The daemon wires readers conditionally, and
// a reader asking a registry nobody built should lose its health signal,
// not take the daemon down with it.
type Registry struct {
	mu        sync.Mutex
	recorders map[string]*Recorder
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{recorders: map[string]*Recorder{}}
}

// For returns the recorder for a source tag, creating it on first use.
// Asking twice returns the same recorder, so a reader that re-registers
// does not start over.
func (g *Registry) For(tag string) *Recorder {
	if g == nil {
		return nil
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.recorders == nil {
		g.recorders = map[string]*Recorder{}
	}
	rec, ok := g.recorders[tag]
	if !ok {
		rec = NewRecorder()
		g.recorders[tag] = rec
	}
	return rec
}

// Polls snapshots every registered reader, in the shape Assess consumes.
func (g *Registry) Polls() map[string]Poll {
	if g == nil {
		return nil
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if len(g.recorders) == 0 {
		return nil
	}
	out := make(map[string]Poll, len(g.recorders))
	for tag, rec := range g.recorders {
		out[tag] = rec.Poll()
	}
	return out
}
