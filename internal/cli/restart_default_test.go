package cli

import (
	"bytes"
	"strings"
	"testing"
)

// A config write that prints "now go restart it" and does not is the same
// silent no-op this codebase keeps finding elsewhere: the operator ran the
// command, the command reported success, and the setting did not take
// effect. Restarting is the rest of doing what was asked.
func TestRestartIsAttemptedByDefault(t *testing.T) {
	var buf bytes.Buffer
	calls := 0
	applyRestartWith(&buf, restartDeps{
		Supervised: true,
		Restart:    func() error { calls++; return nil },
	}, true, false)
	if calls != 1 {
		t.Fatalf("restart called %d times, want 1 — a config write should take effect", calls)
	}
	if !strings.Contains(buf.String(), "restarted the daemon") {
		t.Errorf("want the restart reported: %q", buf.String())
	}
}

// --no-restart is for the operator writing several keys in a row, or
// scripting, who does not want a bounce per key.
func TestNoRestartSkipsAndSaysWhatToRun(t *testing.T) {
	var buf bytes.Buffer
	calls := 0
	applyRestartWith(&buf, restartDeps{
		Supervised: true,
		Restart:    func() error { calls++; return nil },
	}, false, false)
	if calls != 0 {
		t.Fatalf("restart called %d times, want 0", calls)
	}
	if !strings.Contains(buf.String(), "tokenops daemon restart") {
		t.Errorf("want the command named so the operator can run it: %q", buf.String())
	}
}

// Unsupervised there is no unit to bounce, so it falls back to telling the
// operator what to do rather than reporting a restart that did not happen.
func TestUnsupervisedFallsBackToTheHint(t *testing.T) {
	var buf bytes.Buffer
	calls := 0
	applyRestartWith(&buf, restartDeps{
		Supervised: false,
		Restart:    func() error { calls++; return nil },
	}, true, false)
	if calls != 0 {
		t.Fatalf("restart called %d times with no unit installed, want 0", calls)
	}
	out := buf.String()
	if strings.Contains(out, "restarted the daemon") {
		t.Errorf("must not claim a restart that did not happen: %q", out)
	}
	if !strings.Contains(out, "tokenops start") {
		t.Errorf("want the manual instruction: %q", out)
	}
}

// A failed restart is reported as a failure, with the command to retry.
// Swallowing it would leave the operator believing the config was live.
func TestFailedRestartIsReportedNotSwallowed(t *testing.T) {
	var buf bytes.Buffer
	applyRestartWith(&buf, restartDeps{
		Supervised: true,
		Restart:    func() error { return errTestRestart },
	}, true, false)
	out := buf.String()
	if strings.Contains(out, "restarted the daemon;") {
		t.Errorf("a failed restart must not read as success: %q", out)
	}
	if !strings.Contains(out, "could not restart") {
		t.Errorf("want the failure stated: %q", out)
	}
}

// The MCP server is a child of the client that spawned it, so even a
// successful daemon restart leaves the operator one thing to do.
func TestMCPReloadIsStillAskedForAfterARestart(t *testing.T) {
	var buf bytes.Buffer
	applyRestartWith(&buf, restartDeps{
		Supervised: true,
		Restart:    func() error { return nil },
	}, true, true)
	if !strings.Contains(buf.String(), "MCP") {
		t.Errorf("want the MCP reload still requested: %q", buf.String())
	}
}

var errTestRestart = errTest("launchctl: kickstart failed")

type errTest string

func (e errTest) Error() string { return string(e) }
