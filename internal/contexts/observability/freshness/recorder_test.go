package freshness_test

import (
	"errors"
	"sync"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/observability/freshness"
)

// Every poller already tracked its last error, and nothing read any of
// them: four packages each grew a private lastErr field and a LastError
// method with no call sites. None tracked the last *success*, which is
// the fact that separates a refused poller from an unused vendor.
func TestRecorderReportsSuccessAndFailure(t *testing.T) {
	rec := freshness.NewRecorder()
	at := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)

	rec.Succeeded(at)
	got := rec.Poll()
	if !got.LastSuccessAt.Equal(at) || !got.LastAttemptAt.Equal(at) {
		t.Errorf("after a success: %+v", got)
	}
	if got.LastError != nil {
		t.Errorf("a success left an error behind: %v", got.LastError)
	}

	boom := errors.New("401 unauthorized")
	rec.Failed(boom, at.Add(time.Minute))
	got = rec.Poll()
	if got.LastError == nil {
		t.Fatal("the failure was not recorded")
	}
	if !got.LastErrorAt.Equal(at.Add(time.Minute)) {
		t.Errorf("error time = %v", got.LastErrorAt)
	}
	// The earlier success must survive the failure: "last worked at
	// noon, failing since 12:01" is the whole diagnosis.
	if !got.LastSuccessAt.Equal(at) {
		t.Errorf("the failure erased the last success: %+v", got)
	}
	if !got.LastAttemptAt.Equal(at.Add(time.Minute)) {
		t.Errorf("attempt time = %v, want the failed attempt", got.LastAttemptAt)
	}
}

// A success after a failure clears the error, or a source that recovered
// would read as broken forever.
func TestRecoveryClearsTheError(t *testing.T) {
	rec := freshness.NewRecorder()
	at := time.Now()
	rec.Failed(errors.New("timeout"), at)
	rec.Succeeded(at.Add(time.Minute))

	if got := rec.Poll(); got.LastError != nil {
		t.Errorf("a recovered poller still reports %v", got.LastError)
	}
}

// A recorder nothing has reported to is empty, not a claim of success.
func TestUnusedRecorderIsEmpty(t *testing.T) {
	got := freshness.NewRecorder().Poll()
	if !got.LastAttemptAt.IsZero() || !got.LastSuccessAt.IsZero() || got.LastError != nil {
		t.Errorf("a fresh recorder reports %+v", got)
	}
}

// Pollers run on their own goroutines and the status surfaces read from
// another.
func TestRecorderIsSafeUnderConcurrency(t *testing.T) {
	rec := freshness.NewRecorder()
	var wg sync.WaitGroup
	for i := range 50 {
		wg.Add(2)
		go func() { defer wg.Done(); rec.Succeeded(time.Now()) }()
		go func() {
			defer wg.Done()
			if i%2 == 0 {
				rec.Failed(errors.New("boom"), time.Now())
			}
			_ = rec.Poll()
		}()
	}
	wg.Wait()
}

// The registry is what the daemon hands to the status surfaces: one
// place that knows every reader's health, instead of four private fields
// nobody could reach.
func TestRegistryCollectsEveryRecorder(t *testing.T) {
	reg := freshness.NewRegistry()
	at := time.Now()
	reg.For("cursor").Succeeded(at)
	reg.For("claude_usage_meter").Failed(errors.New("401"), at)

	polls := reg.Polls()
	if len(polls) != 2 {
		t.Fatalf("want 2 entries, got %+v", polls)
	}
	if polls["cursor"].LastError != nil {
		t.Errorf("cursor reports an error: %+v", polls["cursor"])
	}
	if polls["claude_usage_meter"].LastError == nil {
		t.Error("the meter's failure was not collected")
	}
}

// For returns the same recorder for a tag, so a poller that asks twice
// does not start over.
func TestRegistryReturnsTheSameRecorderPerTag(t *testing.T) {
	reg := freshness.NewRegistry()
	first := reg.For("cursor")
	second := reg.For("cursor")
	if first != second {
		t.Error("two recorders for one tag")
	}
}

// A nil registry is usable. The daemon wires readers conditionally, and
// a reader that asks a registry nobody built must not panic — losing a
// health signal is a worse outcome than losing the daemon, but panicking
// is worse than both.
func TestNilRegistryIsUsable(t *testing.T) {
	var reg *freshness.Registry
	reg.For("cursor").Succeeded(time.Now()) // must not panic
	if got := reg.Polls(); got != nil {
		t.Errorf("a nil registry returned %+v", got)
	}
}
