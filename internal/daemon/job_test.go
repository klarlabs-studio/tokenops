package daemon

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// stubSupervisor stands in for launchd and systemd: it says the tokenops
// job was loaded from loaded ("" for no job) and records every command
// instead of running it. No test may reach the real supervisor: the job
// these commands address is the daemon of whoever runs the tests.
func stubSupervisor(t *testing.T, loaded string) *[]string {
	t.Helper()
	prevJob, prevRun := jobUnit, runSupervisor
	var calls []string
	jobUnit = func(UnitKind) string { return loaded }
	runSupervisor = func(name string, args ...string) ([]byte, error) {
		calls = append(calls, name+" "+strings.Join(args, " "))
		return nil, nil
	}
	t.Cleanup(func() { jobUnit, runSupervisor = prevJob, prevRun })
	return &calls
}

// unitFiles is a unit of this test's, another one, a symlink to the first,
// and the path of one that is not there.
func unitFiles(t *testing.T) (mine, other, link, gone string) {
	t.Helper()
	dir := t.TempDir()
	mine, other = filepath.Join(dir, "mine.plist"), filepath.Join(dir, "other.plist")
	for _, p := range []string{mine, other} {
		if err := os.WriteFile(p, []byte("unit"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	link = filepath.Join(dir, "link.plist")
	if err := os.Symlink(mine, link); err != nil {
		t.Fatal(err)
	}
	return mine, other, link, filepath.Join(dir, "gone.plist")
}

func TestCheckJob(t *testing.T) {
	mine, other, link, gone := unitFiles(t)
	tests := []struct {
		name         string
		loaded, unit string
		foreign      bool
	}{
		{"the supervisor runs no job", "", mine, false},
		{"the job is this unit's", mine, mine, false},
		{"the job is this unit's, reached through a symlink", link, mine, false},
		{"the unit is addressed through a symlink", mine, link, false},
		{"the job is another unit's", other, mine, true},
		{"the job is another unit's and this one is not written yet", other, gone, true},
		{"the job's unit file is gone", gone, mine, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stubSupervisor(t, tt.loaded)
			err := CheckJob(UnitLaunchd, tt.unit)
			var foreign *ForeignJobError
			if got := errors.As(err, &foreign); got != tt.foreign {
				t.Fatalf("CheckJob = %v, want foreign %v", err, tt.foreign)
			}
			if tt.foreign {
				if foreign.Loaded != tt.loaded || foreign.Unit != tt.unit {
					t.Errorf("error names %+v, want loaded %s and unit %s", foreign, tt.loaded, tt.unit)
				}
				if !strings.Contains(err.Error(), "daemon uninstall --path "+tt.loaded) {
					t.Errorf("the refusal does not say how to remove the other unit: %v", err)
				}
			}
		})
	}
}

// The supervisor has one tokenops job. A command about a unit somewhere
// else (a --path, a --home, a test's temporary home) must not load over,
// restart or unload the job of the unit the operator installed.
func TestLifecycleLeavesAnotherUnitsJobAlone(t *testing.T) {
	mine, other, _, _ := unitFiles(t)
	tests := []struct {
		name string
		act  func() error
		// before is what the supervisor may be asked before the job is
		// known to be another unit's.
		before []string
	}{
		{"launchd load of a changed unit", func() error { return ApplyUnit(UnitLaunchd, mine, true) }, nil},
		{"launchd load of an unchanged unit", func() error { return ApplyUnit(UnitLaunchd, mine, false) }, nil},
		{"launchd unload", func() error { return DisableUnit(UnitLaunchd, mine) }, nil},
		{"launchd restart", func() error { return RestartUnit(UnitLaunchd, mine) }, nil},
		// systemd finds a unit by name: only after a reload does it say
		// which file the name stands for. A reload restarts nothing.
		{"systemd load", func() error { return ApplyUnit(UnitSystemd, mine, true) }, []string{"systemctl --user daemon-reload"}},
		{"systemd unload", func() error { return DisableUnit(UnitSystemd, mine) }, nil},
		{"systemd restart", func() error { return RestartUnit(UnitSystemd, mine) }, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			calls := stubSupervisor(t, other)
			err := tt.act()
			var foreign *ForeignJobError
			if !errors.As(err, &foreign) || foreign.Loaded != other {
				t.Errorf("err = %v, want a refusal naming %s", err, other)
			}
			if !slices.Equal(*calls, tt.before) {
				t.Errorf("the supervisor was asked %q, want %q", *calls, tt.before)
			}
		})
	}
}

func TestLifecycleActsOnItsOwnJob(t *testing.T) {
	mine, _, _, _ := unitFiles(t)
	job := launchdJob()
	tests := []struct {
		name string
		act  func() error
		want []string
	}{
		{"launchd load of an unchanged unit", func() error { return ApplyUnit(UnitLaunchd, mine, false) },
			[]string{"launchctl kickstart -k " + job}},
		{"launchd unload", func() error { return DisableUnit(UnitLaunchd, mine) },
			[]string{"launchctl bootout " + job}},
		{"launchd restart", func() error { return RestartUnit(UnitLaunchd, mine) },
			[]string{"launchctl kickstart -k " + job}},
		{"systemd load", func() error { return ApplyUnit(UnitSystemd, mine, true) }, []string{
			"systemctl --user daemon-reload",
			"systemctl --user enable " + SystemdName,
			"systemctl --user restart " + SystemdName,
		}},
		{"systemd unload", func() error { return DisableUnit(UnitSystemd, mine) },
			[]string{"systemctl --user disable --now " + SystemdName}},
		{"systemd restart", func() error { return RestartUnit(UnitSystemd, mine) },
			[]string{"systemctl --user restart " + SystemdName}},
	}
	for _, loaded := range []string{mine, ""} {
		for _, tt := range tests {
			t.Run(tt.name+", job from "+filepath.Base(loaded), func(t *testing.T) {
				calls := stubSupervisor(t, loaded)
				if err := tt.act(); err != nil {
					t.Fatalf("err = %v", err)
				}
				if !slices.Equal(*calls, tt.want) {
					t.Errorf("the supervisor was asked %q, want %q", *calls, tt.want)
				}
			})
		}
	}
}

// What the supervisors print, as read on a machine running each.
func TestJobUnitReadsWhatTheSupervisorPrints(t *testing.T) {
	const launchdPrint = "gui/501/de.klarlabs.tokenops = {\n" +
		"\tactive count = 1\n" +
		"\tpath = /Users/ada/Library/LaunchAgents/de.klarlabs.tokenops.plist\n" +
		"\ttype = LaunchAgent\n" +
		"\tstate = running\n" +
		"\n" +
		"\tprogram = /opt/homebrew/bin/tokenops\n" +
		"\tenvironment = {\n" +
		"\t\tpath = /not/the/unit\n" +
		"\t}\n" +
		"}\n"
	tests := []struct {
		name   string
		kind   UnitKind
		out    string
		err    error
		want   string
		asking string
	}{
		{"launchd job", UnitLaunchd, launchdPrint, nil,
			"/Users/ada/Library/LaunchAgents/de.klarlabs.tokenops.plist", "launchctl print " + launchdJob()},
		{"launchd, not loaded", UnitLaunchd, "Could not find service", errors.New("exit status 113"), "", ""},
		{"launchd, a deeper path only", UnitLaunchd, "x = {\n\tenvironment = {\n\t\tpath = /not/the/unit\n\t}\n}\n", nil, "", ""},
		{"systemd unit", UnitSystemd, "FragmentPath=/home/ada/.config/systemd/user/tokenops.service\n", nil,
			"/home/ada/.config/systemd/user/tokenops.service",
			"systemctl --user show --property=FragmentPath " + SystemdName},
		{"systemd, no such unit", UnitSystemd, "FragmentPath=\n", nil, "", ""},
		{"systemd, no manager", UnitSystemd, "Failed to connect to bus", errors.New("exit status 1"), "", ""},
		{"no supervisor", UnitKind("initd"), "", nil, "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prev := runSupervisor
			var asked string
			runSupervisor = func(name string, args ...string) ([]byte, error) {
				asked = name + " " + strings.Join(args, " ")
				return []byte(tt.out), tt.err
			}
			t.Cleanup(func() { runSupervisor = prev })
			if got := jobUnit(tt.kind); got != tt.want {
				t.Errorf("jobUnit = %q, want %q", got, tt.want)
			}
			if tt.asking != "" && asked != tt.asking {
				t.Errorf("asked %q, want %q", asked, tt.asking)
			}
		})
	}
}

func TestSameFile(t *testing.T) {
	mine, other, link, gone := unitFiles(t)
	dir := filepath.Dir(mine)
	t.Chdir(dir)
	tests := []struct {
		a, b string
		want bool
	}{
		{mine, mine, true},
		{mine, link, true},
		{mine, "mine.plist", true},
		{mine, filepath.Join(dir, "sub", "..", "mine.plist"), true},
		{gone, filepath.Join(dir, ".", "gone.plist"), true},
		{mine, other, false},
		{mine, gone, false},
	}
	for _, tt := range tests {
		if got := SameFile(tt.a, tt.b); got != tt.want {
			t.Errorf("SameFile(%s, %s) = %v, want %v", tt.a, tt.b, got, tt.want)
		}
	}
}
