package daemon

import (
	"context"
	"testing"
	"time"
)

// flakyStore answers the last-event lookup once, then misses its
// deadline, as a store does when the machine gets busy.
type flakyStore struct {
	calls int
	first map[string]time.Time
}

func (f *flakyStore) CountBySource(context.Context, time.Time, time.Time) (map[string]int64, error) {
	return map[string]int64{}, nil
}

func (f *flakyStore) LastEventBySource(context.Context) (map[string]time.Time, error) {
	f.calls++
	if f.calls == 1 {
		return f.first, nil
	}
	return nil, context.DeadlineExceeded
}

// A source's last event only moves forward, so the previous answer is a
// true lower bound when the lookup misses its deadline. Carrying it keeps
// the next assessment from losing every source's last event at once.
func TestLastSeenCarriesThePreviousAnswerOnFailure(t *testing.T) {
	at := time.Date(2026, 10, 28, 9, 0, 0, 0, time.UTC)
	inner := &flakyStore{first: map[string]time.Time{"codex-jsonl": at}}
	c := carryLastSeen(inner)

	if got, err := c.LastEventBySource(context.Background()); err != nil || !got["codex-jsonl"].Equal(at) {
		t.Fatalf("first answer %v, %v", got, err)
	}
	got, err := c.LastEventBySource(context.Background())
	if err != nil {
		t.Fatalf("a failed lookup with an earlier answer returned %v", err)
	}
	if !got["codex-jsonl"].Equal(at) {
		t.Errorf("carried %v, want the earlier answer %v", got, at)
	}
}

// With no earlier answer there is nothing to carry, and the failure is
// passed on for the assessment to report as unknown.
func TestLastSeenPassesOnAFailureWithNothingToCarry(t *testing.T) {
	inner := &flakyStore{calls: 1}
	if _, err := carryLastSeen(inner).LastEventBySource(context.Background()); err == nil {
		t.Error("a failed first lookup was reported as an answer")
	}
}
