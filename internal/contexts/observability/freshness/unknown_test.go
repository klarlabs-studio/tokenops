package freshness_test

import (
	"errors"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/observability/freshness"
)

// A run of failures keeps the time it began, and a success ends the run.
// Without it, a reader that has failed on every poll since the daemon
// started has no point to count the refusal from: its last success
// predates the daemon, and its last event may be unknown.
func TestRecorderKeepsWhenTheFailuresBegan(t *testing.T) {
	rec := freshness.NewRecorder()
	at := time.Date(2026, 10, 28, 10, 7, 0, 0, time.UTC)

	rec.Failed(errors.New("403"), at)
	rec.Failed(errors.New("403"), at.Add(5*time.Minute))
	if got := rec.Poll().FailingSince; !got.Equal(at) {
		t.Errorf("failing since %v, want the first failure %v", got, at)
	}

	rec.Succeeded(at.Add(10 * time.Minute))
	if got := rec.Poll().FailingSince; !got.IsZero() {
		t.Errorf("a success left failing-since at %v", got)
	}

	rec.Failed(errors.New("403"), at.Add(15*time.Minute))
	if got := rec.Poll().FailingSince; !got.Equal(at.Add(15 * time.Minute)) {
		t.Errorf("a new run of failures began at %v, want %v", got, at.Add(15*time.Minute))
	}
}

// A reader refused on every poll since the daemon started, whose last
// event the store could not say, is failing once the run passes the
// threshold, and not before. Observed 2026-10-28: the claude.ai meter was
// refused every five minutes after a restart while its earlier events
// kept it "healthy", because the refusal was counted from a last success
// and a last event that were both unknown.
func TestARefusalRunCountsFromWhenItBegan(t *testing.T) {
	poll := func(since time.Duration) freshness.Poll {
		return freshness.Poll{
			LastAttemptAt: ago(time.Minute),
			LastError:     errors.New("403 bot check"),
			LastErrorAt:   ago(time.Minute),
			FailingSince:  ago(since),
		}
	}
	for _, tc := range []struct {
		since time.Duration
		want  freshness.State
	}{
		{10 * time.Minute, freshness.StateHealthy},
		{40 * time.Minute, freshness.StateFailing},
	} {
		r := only(t, freshness.Assess(inputs(func(in *freshness.Inputs) {
			in.LastEventAt = nil
			in.LastEventUnknown = true
			in.Polls = map[string]freshness.Poll{"claude_usage_meter": poll(tc.since)}
		})))
		if r.State != tc.want {
			t.Errorf("refused for %v: state %q, want %q", tc.since, r.State, tc.want)
		}
	}
}

// A known last event still decides how long a refusal has run: the
// failures the daemon saw are only the part since it started.
func TestAKnownLastEventStillDatesTheRefusal(t *testing.T) {
	r := only(t, freshness.Assess(inputs(func(in *freshness.Inputs) {
		in.LastEventAt["claude_usage_meter"] = ago(20 * time.Hour)
		in.Polls = map[string]freshness.Poll{"claude_usage_meter": {
			LastAttemptAt: ago(time.Minute),
			LastError:     errors.New("403"),
			LastErrorAt:   ago(time.Minute),
			FailingSince:  ago(2 * time.Minute),
		}}
	})))
	if r.State != freshness.StateFailing {
		t.Errorf("state %q, want failing: refused since an event 20h ago", r.State)
	}
}

// When the store cannot say when a source last produced an event, the
// report must not claim it never did. That claim put "no events have ever
// been ingested" on sources holding thousands, with advice to restart a
// daemon that was running.
func TestAnUnknownLastEventIsNotNeverSeen(t *testing.T) {
	r := only(t, freshness.Assess(inputs(func(in *freshness.Inputs) {
		in.LastEventAt = nil
		in.LastEventUnknown = true
	})))
	if r.NeverSeen {
		t.Error("an unknown last event was reported as never seen")
	}
	if r.State != freshness.StateHealthy {
		t.Errorf("state %q, want healthy: the window's count is known", r.State)
	}
}

// When the store cannot count the window either, silence is not judged:
// a count that could not be read is not a count of zero. The report says
// its counts are unknown, and what the readers themselves report stands.
func TestUnknownCountsDoNotMakeASourceStale(t *testing.T) {
	unknown := func(in *freshness.Inputs) {
		in.EventsInWindow = nil
		in.EventsUnknown = true
		in.LastEventAt = nil
		in.LastEventUnknown = true
	}
	r := only(t, freshness.Assess(inputs(func(in *freshness.Inputs) {
		unknown(in)
		in.OriginNewest = map[string]freshness.Origin{"claude_usage_meter": {At: ago(time.Minute), Known: true}}
	})))
	if r.State == freshness.StateStale {
		t.Error("a source was graded stale on counts the store could not read")
	}
	if !r.EventsUnknown {
		t.Error("the report does not say its counts are unknown")
	}

	r = only(t, freshness.Assess(inputs(func(in *freshness.Inputs) {
		unknown(in)
		in.Stopped = map[string]error{"claude_usage_meter": errors.New("panic")}
	})))
	if r.State != freshness.StateFailing {
		t.Errorf("a stopped reader with unknown counts: state %q, want failing", r.State)
	}

	r = only(t, freshness.Assess(inputs(func(in *freshness.Inputs) {
		unknown(in)
		in.Polls = map[string]freshness.Poll{"claude_usage_meter": {
			LastAttemptAt: ago(time.Minute),
			LastError:     errors.New("401"),
			LastErrorAt:   ago(time.Minute),
			FailingSince:  ago(time.Hour),
		}}
	})))
	if r.State != freshness.StateFailing {
		t.Errorf("a reader refused for an hour with unknown counts: state %q, want failing", r.State)
	}
}
