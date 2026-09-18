package mcp

import (
	"slices"
	"strings"
	"testing"

	"go.klarlabs.de/tokenops/internal/events"
)

// serve and start share nothing but events.db, so when the ingestion daemon
// is absent serve keeps answering every query successfully against a store
// that has stopped being written. During a 27-day outage eleven serve
// processes were running and start was not, and no surface said so.
//
// The staleness check catches the consequence but needs 48 hours and can be
// explained away as "I haven't used it lately". A missing daemon cannot.
func TestDaemonPresenceWarningWhenAbsent(t *testing.T) {
	warn := daemonPresenceWarning(DaemonReport{}, true)

	if warn == "" {
		t.Fatal("no warning when the ingestion daemon is unreachable")
	}
	// It must name the distinction that caused the incident.
	for _, want := range []string{"tokenops start", "does not ingest"} {
		if !strings.Contains(warn, want) {
			t.Errorf("warning does not mention %q: %s", want, warn)
		}
	}
}

func TestDaemonPresenceSilentWhenRunning(t *testing.T) {
	if warn := daemonPresenceWarning(DaemonReport{URL: "http://127.0.0.1:7878", Alive: true}, true); warn != "" {
		t.Errorf("warning emitted while the daemon is reachable: %s", warn)
	}
}

// A nil probe means the caller could not check — that is not evidence of
// absence, and inventing a warning from it would be a false alarm.
func TestDaemonPresenceSilentWithoutAProbe(t *testing.T) {
	if warn := daemonPresenceWarning(DaemonReport{}, false); warn != "" {
		t.Errorf("warning emitted with no probe available: %s", warn)
	}
}

// A daemon that is up and losing rows looks healthy from every other angle:
// ready is true, the store opens, queries answer. The warning is the only
// thing that says the totals are short.
func TestStatusWarnsOnDroppedTelemetryWhileReady(t *testing.T) {
	res := statusInfo(ControlDeps{
		ReadyCheck:  func() bool { return true },
		DaemonProbe: func() DaemonReport { return DaemonReport{URL: "http://127.0.0.1:7878", Alive: true, Dropped: 30254} },
	})
	if !res.Ready {
		t.Fatalf("dropped rows must not make the daemon un-ready: %+v", res)
	}
	if res.State != "degraded" {
		t.Errorf("state = %q, want degraded", res.State)
	}
	if len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "30254") {
		t.Errorf("want one warning naming the count, got %#v", res.Warnings)
	}
	if !slices.Contains(res.NextActions, events.DropNextAction) {
		t.Errorf("want the drop remediation in next_actions, got %#v", res.NextActions)
	}
}

func TestStatusIsSilentWhenNothingWasDropped(t *testing.T) {
	res := statusInfo(ControlDeps{
		ReadyCheck:  func() bool { return true },
		DaemonProbe: func() DaemonReport { return DaemonReport{Alive: true} },
	})
	if len(res.Warnings) != 0 {
		t.Fatalf("want no warnings, got %#v", res.Warnings)
	}
	if res.State != "ready" {
		t.Errorf("state = %q, want ready", res.State)
	}
}

// The probe is one HTTP round trip and statusInfo needs two answers out of
// it. Calling the hook once per answer would double every status call.
func TestStatusProbesTheDaemonExactlyOnce(t *testing.T) {
	calls := 0
	statusInfo(ControlDeps{
		ReadyCheck:  func() bool { return true },
		DaemonProbe: func() DaemonReport { calls++; return DaemonReport{} },
	})
	if calls != 1 {
		t.Fatalf("probe called %d times, want 1", calls)
	}
}
