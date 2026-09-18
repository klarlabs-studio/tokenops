package daemon

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
)

// restartArgv is the command that restarts an already-installed unit.
//
// Separated from running it so the argv is assertable: a "restart" that
// starts a second daemon, or that no-ops against a live job, would report
// success and change nothing — and the config change the operator made to
// prompt the restart would silently not take effect.
func restartArgv(kind UnitKind) ([]string, error) {
	switch kind {
	case UnitLaunchd:
		// kickstart -k is the only launchd verb that stops a running job
		// and starts it again. Plain kickstart against a live job does
		// nothing at all.
		label := "gui/" + strconv.Itoa(os.Getuid()) + "/" + LaunchdLabel
		return []string{"launchctl", "kickstart", "-k", label}, nil
	case UnitSystemd:
		return []string{"systemctl", "--user", "restart", SystemdName}, nil
	default:
		return nil, fmt.Errorf("supervisor: cannot restart kind %q", kind)
	}
}

// RestartUnit restarts the supervised daemon so it re-reads its config.
//
// The daemon loads config once at boot, so every command that writes config
// has to tell the operator to restart it. Until now none of them named a
// command to run, which left "restart the daemon" as an instruction the
// operator had to work out for themselves.
func RestartUnit(kind UnitKind) error {
	argv, err := restartArgv(kind)
	if err != nil {
		return err
	}
	out, err := exec.Command(argv[0], argv[1:]...).CombinedOutput() //nolint:gosec // argv is built from a closed set of kinds
	if err != nil {
		return fmt.Errorf("%s: %w (%s)", argv[0], err, bytesPreview(out))
	}
	return nil
}
