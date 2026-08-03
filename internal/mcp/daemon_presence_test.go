package mcp

import (
	"strings"
	"testing"
)

// serve and start share nothing but events.db, so when the ingestion daemon
// is absent serve keeps answering every query successfully against a store
// that has stopped being written. During a 27-day outage eleven serve
// processes were running and start was not, and no surface said so.
//
// The staleness check catches the consequence but needs 48 hours and can be
// explained away as "I haven't used it lately". A missing daemon cannot.
func TestDaemonPresenceWarningWhenAbsent(t *testing.T) {
	warn := daemonPresenceWarning(func() (string, bool) { return "", false })

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
	if warn := daemonPresenceWarning(func() (string, bool) { return "http://127.0.0.1:7878", true }); warn != "" {
		t.Errorf("warning emitted while the daemon is reachable: %s", warn)
	}
}

// A nil probe means the caller could not check — that is not evidence of
// absence, and inventing a warning from it would be a false alarm.
func TestDaemonPresenceSilentWithoutAProbe(t *testing.T) {
	if warn := daemonPresenceWarning(nil); warn != "" {
		t.Errorf("warning emitted with no probe available: %s", warn)
	}
}
