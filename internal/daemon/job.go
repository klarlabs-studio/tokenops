package daemon

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// The supervisor keeps one tokenops job per user: launchd under the label,
// systemd under the unit name. A unit file can be anywhere (--path, --home,
// a test's temporary home), and the commands that load, restart and unload
// address the job by that one name. So a command about a unit in one place
// acted on the job loaded from another: an install into a temporary home
// replaced the daemon the operator was running.
//
// The rule: a command acts on the job only when the supervisor loaded it
// from the unit the command addresses, or runs none.

// ForeignJobError is a command refused because the job the supervisor runs
// was loaded from another unit file than the one the command addresses.
// Nothing was stopped, started or unloaded.
type ForeignJobError struct {
	// Loaded is the unit file the supervisor's job came from, Unit the one
	// the command addresses.
	Loaded, Unit string
}

func (e *ForeignJobError) Error() string {
	return fmt.Sprintf("the tokenops daemon the supervisor runs was installed from %s, not %s, so it was left as it is "+
		"(`tokenops daemon uninstall --path %s` removes that one)", e.Loaded, e.Unit, e.Loaded)
}

// CheckJob reports whether a command addressing the unit file at unit may
// act on the supervisor's job, with a *ForeignJobError when it may not.
//
// It refuses on what the supervisor says and on nothing else. A supervisor
// that runs no job, or does not name the file its job came from, leaves the
// command to act as it always did; so does a job whose unit file is gone,
// which no command could address any more.
func CheckJob(kind UnitKind, unit string) error {
	loaded := jobUnit(kind)
	if loaded == "" || SameFile(loaded, unit) {
		return nil
	}
	if _, err := os.Stat(loaded); errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return &ForeignJobError{Loaded: loaded, Unit: unit}
}

// jobUnit asks the supervisor which unit file its tokenops job was loaded
// from: "" when it runs none or does not say. It only reads. A var so that
// no test asks the real supervisor.
var jobUnit = func(kind UnitKind) string {
	switch kind {
	case UnitLaunchd:
		out, err := runLaunchctl("print", launchdJob())
		if err != nil {
			return "" // not loaded
		}
		return launchdJobUnit(string(out))
	case UnitSystemd:
		out, err := runSupervisor("systemctl", "--user", "show", "--property=FragmentPath", SystemdName)
		if err != nil {
			return ""
		}
		return systemdJobUnit(string(out))
	default:
		return ""
	}
}

// launchdDomain is the launchd domain of this user's agents, and launchdJob
// the tokenops job in it.
func launchdDomain() string { return "gui/" + strconv.Itoa(os.Getuid()) }

func launchdJob() string { return launchdDomain() + "/" + LaunchdLabel }

// launchdJobUnit reads the plist a job was loaded from out of `launchctl
// print`. The job's own properties are indented by one tab; deeper ones
// (a path among its environment, say) belong to something else.
func launchdJobUnit(print string) string {
	for line := range strings.SplitSeq(print, "\n") {
		if path, ok := strings.CutPrefix(line, "\tpath = "); ok {
			return strings.TrimSpace(path)
		}
	}
	return ""
}

// systemdJobUnit reads the unit file out of `systemctl show
// --property=FragmentPath`: the file the manager would run under the
// unit's name, empty when it knows none.
func systemdJobUnit(show string) string {
	for line := range strings.SplitSeq(show, "\n") {
		if path, ok := strings.CutPrefix(strings.TrimSpace(line), "FragmentPath="); ok {
			return path
		}
	}
	return ""
}

// SameFile reports whether two paths name the same file, however each is
// written: relative, through a symlink (/tmp on macOS), or not there any
// more.
func SameFile(a, b string) bool { return resolvedPath(a) == resolvedPath(b) }

func resolvedPath(p string) string {
	if abs, err := filepath.Abs(p); err == nil {
		p = abs
	}
	if real, err := filepath.EvalSymlinks(p); err == nil {
		return real
	}
	// A file that is gone is still in the directory it was in.
	if dir, err := filepath.EvalSymlinks(filepath.Dir(p)); err == nil {
		return filepath.Join(dir, filepath.Base(p))
	}
	return filepath.Clean(p)
}
