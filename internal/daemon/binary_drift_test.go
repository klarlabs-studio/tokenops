package daemon

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/infra/selfexe"
)

// upgraded is what a check reports after `brew upgrade` repointed the link
// at a new Caskroom directory and removed the old one.
func upgraded(running, installed string) selfexe.Drift {
	return selfexe.Drift{OutOfDate: true, Running: running, Installed: installed}
}

func alwaysExists(string) bool { return true }

// The daemon restarts only once the drift has held for two checks in a row,
// so a brew upgrade caught mid-swap is not acted on.
func TestDriftWatchSettlesBeforeRestarting(t *testing.T) {
	w := &driftWatch{settle: 2, supervised: true, exists: alwaysExists}
	if v := w.Observe(upgraded("/old", "/new")); v.Verdict != driftNone {
		t.Fatalf("first drifted check acted on: %+v", v)
	}
	v := w.Observe(upgraded("/old", "/new"))
	if v.Verdict != driftRestart || v.Target != "/new" || v.Running != "/old" {
		t.Fatalf("settled drift = %+v, want restart onto /new", v)
	}
}

// A check that sees the install current again resets the settle count, so
// a flicker during the swap does not add up across unrelated checks.
func TestDriftWatchSettleResetsWhenDriftClears(t *testing.T) {
	w := &driftWatch{settle: 2, supervised: true, exists: alwaysExists}
	w.Observe(upgraded("/old", "/new"))
	w.Observe(selfexe.Drift{Running: "/old"})
	if v := w.Observe(upgraded("/old", "/new")); v.Verdict != driftNone {
		t.Fatalf("drift that cleared in between still counted: %+v", v)
	}
}

// An in-place replacement (go install, make install) leaves Installed empty;
// the new file is at the running path.
func TestDriftWatchRestartsOntoRunningPathAfterInPlaceReplacement(t *testing.T) {
	w := &driftWatch{settle: 2, supervised: true, exists: alwaysExists}
	w.Observe(selfexe.Drift{OutOfDate: true, Running: "/usr/local/bin/tokenops"})
	v := w.Observe(selfexe.Drift{OutOfDate: true, Running: "/usr/local/bin/tokenops"})
	if v.Verdict != driftRestart || v.Target != "/usr/local/bin/tokenops" {
		t.Fatalf("in-place replacement = %+v", v)
	}
}

// The install is gone: nothing to restart onto. Stay up, say so once, and
// keep checking so the restart happens when a binary finally appears.
func TestDriftWatchStaysUpWhenNewBinaryMissingAndLogsOnce(t *testing.T) {
	present := false
	w := &driftWatch{settle: 2, supervised: true, exists: func(string) bool { return present }}
	w.Observe(upgraded("/old", "/new"))
	first := w.Observe(upgraded("/old", "/new"))
	if first.Verdict != driftUnavailable || first.Target != "/new" {
		t.Fatalf("missing binary = %+v, want unavailable naming /new", first)
	}
	if again := w.Observe(upgraded("/old", "/new")); again.Verdict != driftNone {
		t.Fatalf("missing binary logged twice: %+v", again)
	}
	present = true
	if v := w.Observe(upgraded("/old", "/new")); v.Verdict != driftRestart {
		t.Fatalf("binary appeared but no restart: %+v", v)
	}
}

// A foreground `tokenops start` (a terminal, not a supervisor) warns once and
// never exits: whoever started it owns its lifetime.
func TestDriftWatchForegroundWarnsOnceAndNeverRestarts(t *testing.T) {
	w := &driftWatch{settle: 2, supervised: false, exists: alwaysExists}
	w.Observe(upgraded("/old", "/new"))
	first := w.Observe(upgraded("/old", "/new"))
	if first.Verdict != driftForeground {
		t.Fatalf("foreground drift = %+v, want a warning", first)
	}
	for i := 0; i < 5; i++ {
		if v := w.Observe(upgraded("/old", "/new")); v.Verdict != driftNone {
			t.Fatalf("foreground daemon acted again: %+v", v)
		}
	}
}

func TestDriftWatchCurrentBinaryIsQuiet(t *testing.T) {
	w := &driftWatch{settle: 2, supervised: true, exists: alwaysExists}
	for i := 0; i < 3; i++ {
		if v := w.Observe(selfexe.Drift{Running: "/current"}); v.Verdict != driftNone {
			t.Fatalf("current binary: %+v", v)
		}
	}
}

func TestSupervisedDetectsLaunchdAndSystemdOnly(t *testing.T) {
	env := func(m map[string]string) func(string) string {
		return func(k string) string { return m[k] }
	}
	cases := []struct {
		name string
		env  map[string]string
		want bool
	}{
		{"launchd job", map[string]string{"XPC_SERVICE_NAME": LaunchdLabel}, true},
		{"systemd unit", map[string]string{"INVOCATION_ID": "abc123"}, true},
		{"terminal", map[string]string{"XPC_SERVICE_NAME": "0"}, false},
		{"plain shell", map[string]string{}, false},
	}
	for _, c := range cases {
		if got := supervisedIn(env(c.env)); got != c.want {
			t.Errorf("%s: supervised = %v, want %v", c.name, got, c.want)
		}
	}
}

// The runner is driven by the test's own ticks: no real clock, no sleeps.
func TestWatchBinaryDriftRestartsOnceAndReturns(t *testing.T) {
	checks := []selfexe.Drift{
		{Running: "/old"},
		upgraded("/old", "/new"),
		upgraded("/old", "/new"),
	}
	next := 0
	check := func() selfexe.Drift {
		d := checks[next]
		if next < len(checks)-1 {
			next++
		}
		return d
	}
	ticks := make(chan time.Time)
	var restarted error
	done := make(chan struct{})
	go func() {
		defer close(done)
		watchBinaryDrift(context.Background(), check, &driftWatch{settle: 2, supervised: true, exists: alwaysExists},
			ticks, discardLogger(), func(cause error) { restarted = cause })
	}()
	for range checks {
		ticks <- time.Now()
	}
	<-done
	if !errors.Is(restarted, ErrBinaryReplaced) || !strings.Contains(restarted.Error(), "/new") {
		t.Fatalf("restart cause = %v, want ErrBinaryReplaced naming /new", restarted)
	}
}

func TestWatchBinaryDriftStopsWithContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		watchBinaryDrift(ctx, func() selfexe.Drift { return selfexe.Drift{} },
			&driftWatch{settle: 2, supervised: true, exists: alwaysExists},
			make(chan time.Time), discardLogger(), func(error) { t.Error("restarted without drift") })
	}()
	cancel()
	<-done
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))
}
