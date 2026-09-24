// Package lifecycle supervises the daemon's long-running subsystems.
// A Supervisor names tasks, waits for them during shutdown, bounds that
// wait so a stuck subsystem cannot hold the process open, contains panics,
// and retains task failures for health surfaces and diagnostics.
package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"
)

// ErrStopTimeout is returned by Wait when a task did not stop in time.
// The error names the tasks still running: "shutdown timed out" on its
// own sends an operator to read all nine.
var ErrStopTimeout = errors.New("lifecycle: tasks did not stop in time")

// Supervisor tracks the daemon's long-running tasks.
//
// The zero Supervisor is not usable; call New.
type Supervisor struct {
	// ctx is the daemon's context. Every task receives it, so cancelling
	// it is how the daemon asks all of them to stop.
	ctx    context.Context
	logger *slog.Logger

	mu       sync.Mutex
	wg       sync.WaitGroup
	running  map[string]int
	failed   map[string]error
	stopping bool
}

// New returns a Supervisor whose tasks all receive ctx. Cancelling it is
// how the daemon asks every subsystem to stop.
//
// A nil logger disables logging rather than panicking: losing a log line
// is better than losing the daemon.
func New(ctx context.Context, logger *slog.Logger) *Supervisor {
	if ctx == nil {
		ctx = context.Background()
	}
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	return &Supervisor{
		ctx:     ctx,
		logger:  logger,
		running: map[string]int{},
		failed:  map[string]error{},
	}
}

// Go starts a named task.
//
// After Wait has been called the task is refused rather than started:
// a goroutine begun during shutdown is one nothing will wait for, which
// is the leak this package removes.
func (s *Supervisor) Go(name string, run func(ctx context.Context) error) {
	s.mu.Lock()
	if s.stopping {
		s.mu.Unlock()
		s.logger.Warn("lifecycle: task not started, shutdown in progress", "task", name)
		return
	}
	s.running[name]++
	s.wg.Add(1)
	s.mu.Unlock()

	go func() {
		defer s.wg.Done()
		defer s.finish(name)
		s.record(name, s.runGuarded(name, run))
	}()
}

// runGuarded turns a panic into an error.
//
// A bare `go func()` that panics takes the process with it, so one
// misbehaving poller could stop ingestion entirely. Containing it makes
// a crashing subsystem a reported failure instead.
func (s *Supervisor) runGuarded(name string, run func(ctx context.Context) error) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("lifecycle: task %q panicked: %v", name, r)
		}
	}()
	return run(s.ctx)
}

// record keeps a task's terminal error, ignoring clean cancellation.
//
// A task returning ctx.Err() after being told to stop did what it was
// asked. Recording that as a failure would make every clean shutdown
// look like a crash, and an alarm that fires on every stop is one nobody
// reads.
func (s *Supervisor) record(name string, err error) {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return
	}
	s.mu.Lock()
	s.failed[name] = err
	s.mu.Unlock()
	s.logger.Error("lifecycle: task failed", "task", name, "err", err)
}

func (s *Supervisor) finish(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.running[name] <= 1 {
		delete(s.running, name)
		return
	}
	s.running[name]--
}

// Running names the tasks still alive, sorted so a status table does not
// reshuffle between refreshes.
func (s *Supervisor) Running() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.running))
	for name := range s.running {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// Failed returns the tasks that ended with an error, by name.
//
// This is the control-plane counterpart to ingestion freshness: that
// says whether data is arriving, this says whether the things meant to
// produce it are still alive.
func (s *Supervisor) Failed() map[string]error {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]error, len(s.failed))
	for k, v := range s.failed {
		out[k] = v
	}
	return out
}

// Wait blocks until every task has stopped, or timeout elapses.
//
// The timeout is a bound on one subsystem's bad behaviour, not a
// deadline for the work: a task that ignores cancellation is a bug in
// that task, and is not a reason for the daemon never to exit. The
// returned error names what is still running.
//
// Safe to call more than once.
func (s *Supervisor) Wait(timeout time.Duration) error {
	s.mu.Lock()
	s.stopping = true
	s.mu.Unlock()

	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(timeout):
		stuck := s.Running()
		return fmt.Errorf("%w after %s: %v", ErrStopTimeout, timeout, stuck)
	}

	if failed := s.Failed(); len(failed) > 0 {
		names := make([]string, 0, len(failed))
		for name := range failed {
			names = append(names, name)
		}
		sort.Strings(names)
		return fmt.Errorf("lifecycle: %d task(s) failed: %v", len(failed), names)
	}
	return nil
}
