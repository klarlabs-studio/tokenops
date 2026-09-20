package freshness_test

import (
	"errors"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/observability/freshness"
)

var now = time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)

func ago(d time.Duration) time.Time { return now.Add(-d) }

// inputs builds the common case: one enabled source, healthy, with the
// knobs a test needs to turn.
func inputs(mut func(*freshness.Inputs)) freshness.Inputs {
	in := freshness.Inputs{
		Now:    now,
		Window: 48 * time.Hour,
		Sources: []freshness.Source{
			{Name: "Claude usage meter", Tag: "claude_usage_meter"},
		},
		EventsInWindow: map[string]int64{"claude_usage_meter": 120},
		LastEventAt:    map[string]time.Time{"claude_usage_meter": ago(10 * time.Minute)},
	}
	if mut != nil {
		mut(&in)
	}
	return in
}

func only(t *testing.T, rs []freshness.Report) freshness.Report {
	t.Helper()
	if len(rs) != 1 {
		t.Fatalf("want 1 report, got %d: %+v", len(rs), rs)
	}
	return rs[0]
}

// A source producing events recently is healthy, and says when it was
// last seen. `tokenops vendor-usage status` and tokenops_data_sources
// could report counts but never a last-seen, so "is this still working?"
// had no answer short of reading a warning string.
func TestAProducingSourceIsHealthyAndDated(t *testing.T) {
	r := only(t, freshness.Assess(inputs(nil)))

	if r.State != freshness.StateHealthy {
		t.Errorf("state = %q, want healthy", r.State)
	}
	if r.LastEventAt.IsZero() {
		t.Error("no last-seen was reported")
	}
	if r.SilentFor != 10*time.Minute {
		t.Errorf("silent for %v, want 10m", r.SilentFor)
	}
	if r.Severity != freshness.SeverityOK {
		t.Errorf("severity = %q, want ok", r.Severity)
	}
}

// Quiet with nothing new at the origin is the operator not using the
// vendor, not a broken reader. Reporting it as a fault is how an alarm
// becomes one nobody reads.
func TestAQuietSourceWithNothingNewAtTheOriginIsIdle(t *testing.T) {
	r := only(t, freshness.Assess(inputs(func(in *freshness.Inputs) {
		in.EventsInWindow["claude_usage_meter"] = 0
		in.LastEventAt["claude_usage_meter"] = ago(20 * 24 * time.Hour)
		in.OriginNewest = map[string]freshness.Origin{
			"claude_usage_meter": {At: ago(30 * 24 * time.Hour), Known: true},
		}
	})))

	if r.State != freshness.StateIdle {
		t.Errorf("state = %q, want idle — the origin produced nothing to miss", r.State)
	}
	if r.Severity != freshness.SeverityOK {
		t.Errorf("an unused vendor was graded %q", r.Severity)
	}
}

// Quiet while the origin *does* have newer data is a reader that died.
// This is the case the whole check exists for.
func TestASourceMissingDataItsOriginHasIsStale(t *testing.T) {
	r := only(t, freshness.Assess(inputs(func(in *freshness.Inputs) {
		in.EventsInWindow["claude_usage_meter"] = 0
		in.LastEventAt["claude_usage_meter"] = ago(20 * 24 * time.Hour)
		in.OriginNewest = map[string]freshness.Origin{
			"claude_usage_meter": {At: ago(1 * time.Hour), Known: true},
		}
	})))

	if r.State != freshness.StateStale {
		t.Errorf("state = %q, want stale", r.State)
	}
	if r.Severity != freshness.SeverityCritical {
		t.Errorf("severity = %q, want critical after 20 days", r.Severity)
	}
}

// An origin that cannot be read is an unknown, not a clean bill of
// health: the time-based judgement stands.
func TestAnUnreadableOriginDoesNotClearTheWarning(t *testing.T) {
	r := only(t, freshness.Assess(inputs(func(in *freshness.Inputs) {
		in.EventsInWindow["claude_usage_meter"] = 0
		in.LastEventAt["claude_usage_meter"] = ago(10 * 24 * time.Hour)
		in.OriginNewest = map[string]freshness.Origin{
			"claude_usage_meter": {Known: false},
		}
	})))

	if r.State != freshness.StateStale {
		t.Errorf("state = %q, want stale when the origin cannot be read", r.State)
	}
	if r.Severity != freshness.SeverityDegraded {
		t.Errorf("severity = %q, want degraded after 10 days", r.Severity)
	}
}

// The distinction Phase 2 adds. A poller running every minute and
// getting 401 every time looks identical, from event counts alone, to a
// vendor the operator stopped using — both produce nothing. The poll
// record is what separates them, and it is the failure mode that
// actually happened: one machine logged 3,019 consecutive failures into
// a file nobody read.
func TestAFailingPollerIsDistinguishableFromAnUnusedVendor(t *testing.T) {
	r := only(t, freshness.Assess(inputs(func(in *freshness.Inputs) {
		in.EventsInWindow["claude_usage_meter"] = 0
		in.LastEventAt["claude_usage_meter"] = ago(3 * 24 * time.Hour)
		// The origin says nothing new, which alone would read as idle.
		in.OriginNewest = map[string]freshness.Origin{
			"claude_usage_meter": {At: ago(30 * 24 * time.Hour), Known: true},
		}
		in.Polls = map[string]freshness.Poll{
			"claude_usage_meter": {
				LastAttemptAt: ago(1 * time.Minute),
				LastSuccessAt: ago(3 * 24 * time.Hour),
				LastError:     errors.New("401 unauthorized"),
				LastErrorAt:   ago(1 * time.Minute),
			},
		}
	})))

	if r.State != freshness.StateFailing {
		t.Errorf("state = %q, want failing — the poller is alive and being refused", r.State)
	}
	if r.LastError == "" {
		t.Error("the error was not carried to the caller")
	}
	if r.Severity == freshness.SeverityOK {
		t.Error("a poller being refused was graded ok")
	}
}

// A poller succeeding but seeing an unchanged snapshot is healthy. The
// vendor simply has nothing new, and the successful poll proves the
// credential still works.
func TestASucceedingPollerWithNoNewDataIsIdleNotFailing(t *testing.T) {
	r := only(t, freshness.Assess(inputs(func(in *freshness.Inputs) {
		in.EventsInWindow["claude_usage_meter"] = 0
		in.LastEventAt["claude_usage_meter"] = ago(3 * 24 * time.Hour)
		in.OriginNewest = map[string]freshness.Origin{
			"claude_usage_meter": {At: ago(30 * 24 * time.Hour), Known: true},
		}
		in.Polls = map[string]freshness.Poll{
			"claude_usage_meter": {
				LastAttemptAt: ago(1 * time.Minute),
				LastSuccessAt: ago(1 * time.Minute),
			},
		}
	})))

	if r.State != freshness.StateIdle {
		t.Errorf("state = %q, want idle — the last poll succeeded", r.State)
	}
	if r.LastSuccessfulPollAt.IsZero() {
		t.Error("the successful poll was not reported")
	}
}

// Never having ingested anything is a different fact from having
// stopped, and the two need different advice.
func TestASourceThatNeverProducedAnythingSaysSo(t *testing.T) {
	r := only(t, freshness.Assess(inputs(func(in *freshness.Inputs) {
		in.EventsInWindow["claude_usage_meter"] = 0
		in.LastEventAt = map[string]time.Time{}
	})))

	if !r.LastEventAt.IsZero() {
		t.Errorf("a never-seen source reported a last-seen of %v", r.LastEventAt)
	}
	if r.SilentFor != 0 {
		t.Errorf("silent-for = %v, want 0 for a source that never produced", r.SilentFor)
	}
	if !r.NeverSeen {
		t.Error("NeverSeen is false for a source with no events at all")
	}
}

// Severity escalates with the gap. A warning that reads the same on day
// two and day twenty-seven is one that gets tuned out — which is how a
// 27-day outage went unnoticed.
func TestSeverityEscalatesWithTheGap(t *testing.T) {
	cases := []struct {
		silent time.Duration
		want   string
	}{
		{2 * 24 * time.Hour, freshness.SeverityWarning},
		{8 * 24 * time.Hour, freshness.SeverityDegraded},
		{20 * 24 * time.Hour, freshness.SeverityCritical},
	}
	for _, tc := range cases {
		t.Run(tc.want, func(t *testing.T) {
			r := only(t, freshness.Assess(inputs(func(in *freshness.Inputs) {
				in.EventsInWindow["claude_usage_meter"] = 0
				in.LastEventAt["claude_usage_meter"] = now.Add(-tc.silent)
			})))
			if r.Severity != tc.want {
				t.Errorf("silent %v graded %q, want %q", tc.silent, r.Severity, tc.want)
			}
		})
	}
}

// Assess reports on every enabled source, healthy ones included. The
// old check returned only the stale ones, so "everything is fine" and
// "nothing is configured" were the same empty list.
func TestEverySourceIsReportedNotOnlyTheBrokenOnes(t *testing.T) {
	rs := freshness.Assess(inputs(func(in *freshness.Inputs) {
		in.Sources = append(in.Sources, freshness.Source{Name: "Cursor", Tag: "cursor"})
		in.EventsInWindow["cursor"] = 0
		in.LastEventAt["cursor"] = ago(9 * 24 * time.Hour)
	}))
	if len(rs) != 2 {
		t.Fatalf("want a report per source, got %d", len(rs))
	}
	var healthy, degraded int
	for _, r := range rs {
		switch r.Severity {
		case freshness.SeverityOK:
			healthy++
		case freshness.SeverityDegraded:
			degraded++
		}
	}
	if healthy != 1 || degraded != 1 {
		t.Errorf("want one healthy and one degraded, got %+v", rs)
	}
}

// No configured sources is not an error and not a fault.
func TestNoSourcesYieldsNoReports(t *testing.T) {
	if got := freshness.Assess(freshness.Inputs{Now: now}); len(got) != 0 {
		t.Errorf("want no reports, got %+v", got)
	}
}

// Reports come back in a stable order so a CLI table and a dashboard do
// not reshuffle between refreshes.
func TestReportsAreOrderedByTag(t *testing.T) {
	rs := freshness.Assess(inputs(func(in *freshness.Inputs) {
		in.Sources = []freshness.Source{
			{Name: "Cursor", Tag: "cursor"},
			{Name: "Anthropic", Tag: "anthropic_admin"},
			{Name: "Claude usage meter", Tag: "claude_usage_meter"},
		}
	}))
	want := []string{"anthropic_admin", "claude_usage_meter", "cursor"}
	for i, r := range rs {
		if r.Tag != want[i] {
			t.Errorf("position %d = %q, want %q", i, r.Tag, want[i])
		}
	}
}

// Zero window falls back to the default rather than grading everything
// stale, which is what a caller that forgot to set it would otherwise do.
func TestZeroWindowUsesTheDefault(t *testing.T) {
	r := only(t, freshness.Assess(inputs(func(in *freshness.Inputs) {
		in.Window = 0
	})))
	if r.Window != freshness.DefaultWindow {
		t.Errorf("window = %v, want the default %v", r.Window, freshness.DefaultWindow)
	}
}

// A reader that exited is a stronger fact than a reader whose last poll
// failed: it is not coming back on its own. The daemon ran nine pollers
// as bare goroutines, so one dying logged a line and vanished while
// every surface kept reporting health — the supervisor now knows, and
// this is where that knowledge becomes an answer.
func TestAStoppedReaderIsReportedAsFailing(t *testing.T) {
	r := only(t, freshness.Assess(inputs(func(in *freshness.Inputs) {
		// Everything else says healthy: events arrived inside the window.
		in.Stopped = map[string]error{
			"claude_usage_meter": errors.New("panic: nil map write"),
		}
	})))

	if r.State != freshness.StateFailing {
		t.Errorf("state = %q; a source whose reader exited was not reported "+
			"as failing", r.State)
	}
	if r.LastError == "" {
		t.Error("the reader's exit error did not reach the caller")
	}
	if r.Severity == freshness.SeverityOK {
		t.Error("a dead reader was graded ok")
	}
}

// A reader that exited outranks recent events. Events in the window are
// from before it died, and reporting healthy on the strength of them is
// exactly the delay that let an outage run for weeks.
func TestAStoppedReaderOutranksRecentEvents(t *testing.T) {
	r := only(t, freshness.Assess(inputs(func(in *freshness.Inputs) {
		in.EventsInWindow["claude_usage_meter"] = 5000
		in.LastEventAt["claude_usage_meter"] = ago(time.Minute)
		in.Stopped = map[string]error{"claude_usage_meter": errors.New("exited")}
	})))

	if r.Healthy() {
		t.Error("a source with a dead reader reported itself healthy")
	}
}

// A source with no supervised reader — a file tailer, or one the daemon
// never started — is unaffected.
func TestSourcesWithoutAStoppedReaderAreUnaffected(t *testing.T) {
	r := only(t, freshness.Assess(inputs(func(in *freshness.Inputs) {
		in.Stopped = map[string]error{"some-other-source": errors.New("exited")}
	})))
	if r.State != freshness.StateHealthy {
		t.Errorf("state = %q, want healthy", r.State)
	}
}

// A reader that exited is graded at least degraded however recently it
// was working. Severity normally escalates with the size of the gap,
// but a dead reader has no gap yet and will never close the one it is
// about to open — grading it "warning" alongside a source that is merely
// quiet understates it.
func TestAStoppedReaderIsAtLeastDegraded(t *testing.T) {
	r := only(t, freshness.Assess(inputs(func(in *freshness.Inputs) {
		in.LastEventAt["claude_usage_meter"] = ago(time.Minute)
		in.Stopped = map[string]error{"claude_usage_meter": errors.New("exited")}
	})))
	if r.Severity != freshness.SeverityDegraded {
		t.Errorf("severity = %q, want degraded for a reader that exited", r.Severity)
	}
}

// A long-dead reader still escalates to critical: the two rules
// compose rather than one capping the other.
func TestALongStoppedReaderStillReachesCritical(t *testing.T) {
	r := only(t, freshness.Assess(inputs(func(in *freshness.Inputs) {
		in.EventsInWindow["claude_usage_meter"] = 0
		in.LastEventAt["claude_usage_meter"] = ago(20 * 24 * time.Hour)
		in.Stopped = map[string]error{"claude_usage_meter": errors.New("exited")}
	})))
	if r.Severity != freshness.SeverityCritical {
		t.Errorf("severity = %q, want critical", r.Severity)
	}
}
