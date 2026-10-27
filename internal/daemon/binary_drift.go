package daemon

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"go.klarlabs.de/tokenops/internal/infra/selfexe"
	"go.klarlabs.de/tokenops/internal/version"
)

// driftCheckEvery is how often the daemon looks at its own binary. A check is
// a few stats and readlinks.
const driftCheckEvery = 30 * time.Second

// driftSettle is how many consecutive checks must see the install replaced
// before the daemon acts. A brew upgrade repoints the link after the new
// binary is in place, but a check can still land mid-swap; two checks in a
// row (about a minute) is past any swap.
const driftSettle = 2

// ErrBinaryReplaced is the cause the daemon stops with when the binary it
// runs has been replaced by an install. The process exits non-zero so the
// supervisor (launchd KeepAlive, systemd Restart=on-failure) starts the
// installed binary.
var ErrBinaryReplaced = errors.New("binary replaced by an install")

// supervisedIn reports whether this process was started by the tokenops
// supervisor unit rather than a terminal. launchd gives every job its label
// as XPC_SERVICE_NAME. systemd sets INVOCATION_ID for every unit it starts,
// but desktop terminals often run as units themselves and their shells
// inherit it, so on systemd the process must also sit in tokenops.service's
// cgroup (cgroup is /proc/self/cgroup's content; empty elsewhere). The unit
// template needs no marker and existing unit files stay as they are.
func supervisedIn(getenv func(string) string, cgroup string) bool {
	if getenv("XPC_SERVICE_NAME") == LaunchdLabel {
		return true
	}
	return getenv("INVOCATION_ID") != "" && inSystemdUnit(cgroup, SystemdName)
}

// inSystemdUnit reports whether a /proc/self/cgroup listing places the
// process in the named unit's cgroup.
func inSystemdUnit(cgroup, unit string) bool {
	for _, line := range strings.Split(cgroup, "\n") {
		if strings.HasSuffix(strings.TrimSpace(line), "/"+unit) {
			return true
		}
	}
	return false
}

// selfCgroup reads this process's cgroup listing; empty where there is none.
func selfCgroup() string {
	b, err := os.ReadFile("/proc/self/cgroup")
	if err != nil {
		return ""
	}
	return string(b)
}

// driftVerdict is what one check of the binary calls for.
type driftVerdict int

const (
	// driftNone: nothing to report or do.
	driftNone driftVerdict = iota
	// driftForeground: replaced under a terminal-run daemon, which warns and
	// keeps running.
	driftForeground
	// driftUnavailable: replaced, but the new binary is not there (yet). The
	// daemon stays up and keeps checking.
	driftUnavailable
	// driftRestart: replaced under a supervised daemon with the new binary in
	// place. Stop and let the supervisor start it.
	driftRestart
)

// driftStep is the result of one check: the verdict, plus the paths the
// log line names.
type driftStep struct {
	Verdict driftVerdict
	Running string
	Target  string
}

// driftWatch decides what each check of the binary means. It has no clock
// and no I/O of its own: the runner feeds it checks and the test feeds it
// directly.
type driftWatch struct {
	settle     int
	supervised bool
	// exists reports whether a path is an executable file.
	exists func(path string) bool
	// streak counts consecutive checks that saw the install replaced.
	streak int
	// noted records that this drift episode was already logged, so the
	// warning is printed once and not on every check.
	noted bool
}

// Observe folds one check into the watch and says what, if anything, to do.
func (w *driftWatch) Observe(d selfexe.Drift) driftStep {
	if !d.OutOfDate {
		w.streak, w.noted = 0, false
		return driftStep{}
	}
	w.streak++
	if w.streak < w.settle {
		return driftStep{}
	}
	target := d.Installed
	if target == "" {
		target = d.Running
	}
	step := driftStep{Running: d.Running, Target: target}
	if !w.supervised {
		return w.once(step, driftForeground)
	}
	if !w.exists(target) {
		return w.once(step, driftUnavailable)
	}
	step.Verdict = driftRestart
	return step
}

// once returns verdict the first time it applies in an episode and driftNone
// after that.
func (w *driftWatch) once(step driftStep, verdict driftVerdict) driftStep {
	if w.noted {
		return driftStep{}
	}
	w.noted = true
	step.Verdict = verdict
	return step
}

// isExecutableFile reports whether path is a regular file with an execute bit.
func isExecutableFile(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.Mode().IsRegular() && st.Mode().Perm()&0o111 != 0
}

// watchBinaryDrift runs the watch on every tick until ctx ends. On a restart
// verdict it calls restart with ErrBinaryReplaced wrapped with the target
// and returns, so the caller can begin the normal shutdown.
func watchBinaryDrift(ctx context.Context, check func() selfexe.Drift, w *driftWatch, ticks <-chan time.Time, logger *slog.Logger, restart func(cause error)) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticks:
		}
		step := w.Observe(check())
		switch step.Verdict {
		case driftForeground:
			logger.Warn("tokenops was replaced by an install while this daemon runs the old binary; restart it to run the new one",
				"running", step.Running, "running_version", version.Version, "installed", step.Target)
		case driftUnavailable:
			logger.Warn("tokenops was replaced, but the installed binary is not there yet; staying up and checking again",
				"running", step.Running, "running_version", version.Version, "installed", step.Target)
		case driftRestart:
			logger.Info("tokenops was replaced by an install; stopping so the supervisor starts the installed binary",
				"running", step.Running, "running_version", version.Version, "installed", step.Target)
			restart(fmt.Errorf("%w: %s", ErrBinaryReplaced, step.Target))
			return
		}
	}
}
