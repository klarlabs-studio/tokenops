package daemon

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"time"
)

// ApplyUnit makes the supervisor run the unit at path, replacing whatever
// it ran before, so the daemon comes up on the binary now installed.
// changed says whether the unit file was just rewritten.
//
// It is what `daemon install` runs, and what an upgrade runs after
// replacing the binary. Neither may leave the operator with supervisor
// commands to type: a reinstall that stops the old daemon and fails to
// start the new one leaves no ingestion at all.
func ApplyUnit(kind UnitKind, path string, changed bool) error {
	switch kind {
	case UnitLaunchd:
		domain := "gui/" + strconv.Itoa(os.Getuid())
		return applyLaunchd(runLaunchctl, time.Sleep, domain, domain+"/"+LaunchdLabel, path, changed, launchdUnloadTimeout)
	case UnitSystemd:
		// enable does not restart a unit that is already running, so an
		// upgrade would keep the old binary; restart starts a stopped one
		// and replaces a running one.
		for _, args := range [][]string{
			{"--user", "daemon-reload"},
			{"--user", "enable", SystemdName},
			{"--user", "restart", SystemdName},
		} {
			if out, err := exec.Command("systemctl", args...).CombinedOutput(); err != nil { //nolint:gosec // fixed verbs and unit name
				return fmt.Errorf("systemctl %s: %w (%s)", args[1], err, bytesPreview(out))
			}
		}
		return nil
	default:
		return fmt.Errorf("supervisor: cannot load kind %q", kind)
	}
}

// DisableUnit unloads the unit. Missing/not-loaded is success.
func DisableUnit(kind UnitKind, path string) error {
	_ = path
	switch kind {
	case UnitLaunchd:
		uid := strconv.Itoa(os.Getuid())
		label := "gui/" + uid + "/" + LaunchdLabel
		if out, err := exec.Command("launchctl", "bootout", label).CombinedOutput(); err != nil {
			// launchctl bootout exits non-zero when the service is not loaded.
			_ = out
		}
		return nil
	case UnitSystemd:
		_ = exec.Command("systemctl", "--user", "disable", "--now", SystemdName).Run()
		return nil
	default:
		return fmt.Errorf("supervisor: cannot unload kind %q", kind)
	}
}

// launchdUnloadTimeout bounds the wait for a previous job to unload. It
// sits above launchd's default ExitTimeOut (20s), after which launchd
// SIGKILLs a job that has not exited, so a job that is merely slow to
// drain always makes it.
const launchdUnloadTimeout = 30 * time.Second

// launchdPollInterval is how often the unload wait re-checks the label.
const launchdPollInterval = 200 * time.Millisecond

func runLaunchctl(args ...string) ([]byte, error) {
	return exec.Command("launchctl", args...).CombinedOutput() //nolint:gosec // args are built from fixed verbs, the label and the unit path
}

// applyLaunchd makes launchd run the unit at path.
//
// An unchanged unit whose job is loaded only needs its process replaced:
// kickstart -k does that in place, and the new binary behind the stable
// path comes up. That is every upgrade.
//
// Otherwise the job is unloaded and the unit loaded afresh. bootout returns
// before launchd has let go of the label — the old job is still shutting
// down, and the daemon drains its event queue on the way out — and a
// bootstrap in that window fails with "Bootstrap failed: 5: Input/output
// error". Every reinstall over a running daemon did exactly that, and left
// no daemon running. So wait for the label to go, and give bootstrap the
// same window: launchd can refuse it briefly after print stops seeing it.
func applyLaunchd(run func(...string) ([]byte, error), sleep func(time.Duration), domain, label, path string, changed bool, timeout time.Duration) error {
	if !changed {
		if _, err := run("kickstart", "-k", label); err == nil {
			return nil
		}
		// Not loaded: fall through and load it.
	}
	// bootout is best-effort: first install has nothing to unload.
	_, _ = run("bootout", label)
	waited := time.Duration(0)
	for ; ; waited += launchdPollInterval {
		if _, err := run("print", label); err != nil {
			break // not loaded
		}
		if waited >= timeout {
			return fmt.Errorf("the previous tokenops daemon was still shutting down after %s", timeout)
		}
		sleep(launchdPollInterval)
	}
	for {
		out, err := run("bootstrap", domain, path)
		if err == nil {
			break
		}
		if waited >= timeout {
			return fmt.Errorf("launchctl bootstrap: %w (%s)", err, bytesPreview(out))
		}
		sleep(launchdPollInterval)
		waited += launchdPollInterval
	}
	if out, err := run("enable", label); err != nil {
		return fmt.Errorf("launchctl enable: %w (%s)", err, bytesPreview(out))
	}
	if out, err := run("kickstart", "-k", label); err != nil {
		return fmt.Errorf("launchctl kickstart: %w (%s)", err, bytesPreview(out))
	}
	return nil
}

func bytesPreview(b []byte) string {
	s := string(b)
	if len(s) > 240 {
		return s[:240] + "…"
	}
	return s
}
