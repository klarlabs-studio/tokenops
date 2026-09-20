package cursor

import (
	"errors"
	"testing"

	"go.klarlabs.de/tokenops/internal/contexts/observability/freshness"
)

// The poller tracked lastErr in a private field behind a LastError
// method with no call sites: the information existed and reached nobody,
// which is the same shape as the outage it was meant to prevent. It also
// never recorded a *success*, so a poller being refused every minute was
// indistinguishable, from the outside, from a vendor nobody uses.
func TestPollerReportsItsHealth(t *testing.T) {
	rec := freshness.NewRecorder()
	p := NewPoller(nil, PollerOptions{Health: rec})

	p.recordSuccess()
	if got := rec.Poll(); got.LastSuccessAt.IsZero() {
		t.Errorf("a successful poll was not reported: %+v", got)
	}

	p.recordErr(errors.New("401 unauthorized"))
	got := rec.Poll()
	if got.LastError == nil {
		t.Error("a failed poll was not reported")
	}
	if got.LastSuccessAt.IsZero() {
		t.Error("the failure erased when the poller last worked")
	}
}

// A poller wired without a recorder must behave exactly as before.
func TestPollerWithoutARecorderStillWorks(t *testing.T) {
	p := NewPoller(nil, PollerOptions{})
	p.recordSuccess()
	p.recordErr(errors.New("boom"))
	if _, err := p.LastError(); err == nil {
		t.Error("the existing LastError path stopped working")
	}
}
