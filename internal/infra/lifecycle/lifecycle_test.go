package lifecycle_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/infra/lifecycle"
)

func sup(t *testing.T) *lifecycle.Supervisor {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	return lifecycle.New(ctx, nil)
}

// supCancel returns a supervisor and the cancel that stops its tasks.
func supCancel(t *testing.T) (*lifecycle.Supervisor, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	return lifecycle.New(ctx, nil), cancel
}

// The daemon starts nine goroutines with bare `go func()`. Nothing waits
// for them, nothing knows their names, and a subsystem that dies logs a
// line into a file nobody reads and is never mentioned again. Shutdown
// returns while they are still running.
func TestShutdownWaitsForEveryTask(t *testing.T) {
	s, cancel := supCancel(t)
	var stopped atomic.Int32

	for range 5 {
		s.Go("worker", func(ctx context.Context) error {
			<-ctx.Done()
			time.Sleep(20 * time.Millisecond) // draining
			stopped.Add(1)
			return nil
		})
	}

	cancel()
	if err := s.Wait(2 * time.Second); err != nil {
		t.Fatalf("wait: %v", err)
	}
	if got := stopped.Load(); got != 5 {
		t.Errorf("Wait returned with %d of 5 tasks drained", got)
	}
}

// A task that dies on its own must be visible. Today the daemon's
// goroutines log a warning and vanish; the subsystem is gone and every
// surface still reports the daemon healthy.
func TestAFailedTaskIsReported(t *testing.T) {
	s := sup(t)
	boom := errors.New("poller exploded")

	s.Go("cursor-poller", func(context.Context) error { return boom })

	if err := s.Wait(2 * time.Second); err == nil {
		t.Fatal("a failing task produced a clean shutdown")
	}
	failed := s.Failed()
	if len(failed) != 1 {
		t.Fatalf("failed = %+v", failed)
	}
	if !errors.Is(failed["cursor-poller"], boom) {
		t.Errorf("the error was not kept: %+v", failed)
	}
}

// Context cancellation is how the daemon stops. A task returning
// ctx.Err() after being told to stop did what it was asked, and must not
// be reported as a failure — otherwise every clean shutdown looks like a
// crash.
func TestCancellationIsNotAFailure(t *testing.T) {
	s, cancel := supCancel(t)

	s.Go("poller", func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	})
	cancel()

	if err := s.Wait(2 * time.Second); err != nil {
		t.Errorf("a cancelled task was reported as a failure: %v", err)
	}
	if len(s.Failed()) != 0 {
		t.Errorf("failed = %+v", s.Failed())
	}
}

// The daemon must not hang forever because one subsystem will not stop.
// A task that ignores cancellation is a bug in that task, not a reason
// to never exit.
func TestWaitGivesUpOnAStuckTask(t *testing.T) {
	s := sup(t)
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })

	s.Go("stuck", func(context.Context) error {
		<-release
		return nil
	})

	err := s.Wait(50 * time.Millisecond)
	if err == nil {
		t.Fatal("Wait returned cleanly with a task still running")
	}
	// The error has to name what is stuck; "shutdown timed out" sends an
	// operator to read all nine.
	if !errors.Is(err, lifecycle.ErrStopTimeout) {
		t.Errorf("err = %v, want ErrStopTimeout", err)
	}
	if got := s.Running(); len(got) != 1 || got[0] != "stuck" {
		t.Errorf("running = %v, want [stuck]", got)
	}
}

// Which subsystems are up is a question nothing could answer. It is the
// control-plane half of the freshness work: ingestion health said
// whether data was arriving, and this says whether the things meant to
// produce it are alive.
func TestRunningTasksAreNamed(t *testing.T) {
	s := sup(t)
	started := make(chan struct{})

	s.Go("claude-usage-meter", func(ctx context.Context) error {
		close(started)
		<-ctx.Done()
		return nil
	})
	<-started

	running := s.Running()
	if len(running) != 1 || running[0] != "claude-usage-meter" {
		t.Errorf("running = %v", running)
	}
}

// Running is sorted, so a status table does not reshuffle between
// refreshes — the same reason the freshness reports are.
func TestRunningIsSorted(t *testing.T) {
	s := sup(t)

	ready := make(chan struct{}, 3)
	for _, name := range []string{"cursor", "anthropic", "copilot"} {
		s.Go(name, func(ctx context.Context) error {
			ready <- struct{}{}
			<-ctx.Done()
			return nil
		})
	}
	for range 3 {
		<-ready
	}

	got := s.Running()
	want := []string{"anthropic", "copilot", "cursor"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("running = %v, want %v", got, want)
		}
	}
}

// A supervisor with nothing to supervise stops immediately rather than
// waiting out its timeout.
func TestEmptySupervisorStopsAtOnce(t *testing.T) {
	start := time.Now()
	if err := sup(t).Wait(5 * time.Second); err != nil {
		t.Fatalf("wait: %v", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("an empty supervisor took %v to stop", elapsed)
	}
}

// A panicking task must not take the daemon with it. One subsystem
// crashing should be a reported failure, not a process exit — the
// current bare goroutines have no recovery at all.
func TestAPanickingTaskIsContainedAndReported(t *testing.T) {
	s := sup(t)
	s.Go("panicky", func(context.Context) error { panic("boom") })

	err := s.Wait(2 * time.Second)
	if err == nil {
		t.Fatal("a panicking task produced a clean shutdown")
	}
	if len(s.Failed()) != 1 {
		t.Errorf("failed = %+v", s.Failed())
	}
	if s.Failed()["panicky"] == nil {
		t.Error("the panic was not recorded against its task")
	}
}

// Wait is called once from the shutdown path, but a second call must be
// safe rather than hanging or double-closing.
func TestWaitIsSafeToCallTwice(t *testing.T) {
	s := sup(t)
	s.Go("quick", func(context.Context) error { return nil })

	if err := s.Wait(time.Second); err != nil {
		t.Fatalf("first wait: %v", err)
	}
	if err := s.Wait(time.Second); err != nil {
		t.Fatalf("second wait: %v", err)
	}
}

// Starting a task after shutdown has begun must be refused rather than
// leaking a goroutine nothing will wait for.
func TestGoAfterWaitIsRefused(t *testing.T) {
	s := sup(t)
	if err := s.Wait(time.Second); err != nil {
		t.Fatalf("wait: %v", err)
	}

	var ran atomic.Bool
	s.Go("late", func(context.Context) error {
		ran.Store(true)
		return nil
	})
	if ran.Load() {
		t.Error("a task started after shutdown")
	}
	if len(s.Running()) != 0 {
		t.Errorf("running = %v", s.Running())
	}
}
