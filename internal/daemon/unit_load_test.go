package daemon

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"
)

// fakeLaunchd models the part of launchd that broke `daemon install`:
// bootout returns at once, but the label stays loaded while the old job
// shuts down, and bootstrap of a still-loaded label fails with error 5.
type fakeLaunchd struct {
	loaded bool
	// unloadAfter is how many `print` calls still see the label after a
	// bootout — the old job still shutting down.
	unloadAfter int
	unloading   bool
	// refuseBootstraps is how many bootstraps launchd still rejects with
	// error 5 after print has stopped seeing the label.
	refuseBootstraps int
	calls            [][]string
}

func (f *fakeLaunchd) run(args ...string) ([]byte, error) {
	f.calls = append(f.calls, args)
	switch args[0] {
	case "bootout":
		if !f.loaded {
			return nil, errors.New("exit status 3")
		}
		f.unloading = true
		return nil, nil
	case "print":
		if f.unloading {
			if f.unloadAfter == 0 {
				f.loaded, f.unloading = false, false
			} else {
				f.unloadAfter--
			}
		}
		if !f.loaded {
			return []byte("Could not find service"), errors.New("exit status 113")
		}
		return []byte("state = running"), nil
	case "bootstrap":
		if f.loaded || f.refuseBootstraps > 0 {
			f.refuseBootstraps--
			return []byte("Bootstrap failed: 5: Input/output error"), errors.New("exit status 5")
		}
		f.loaded = true
		return nil, nil
	case "kickstart":
		if !f.loaded {
			return []byte("Could not find service"), errors.New("exit status 113")
		}
		return nil, nil
	}
	return nil, nil
}

func (f *fakeLaunchd) verbs() []string {
	v := make([]string, 0, len(f.calls))
	for _, c := range f.calls {
		v = append(v, c[0])
	}
	return v
}

func noSleep(time.Duration) {}

// Reinstalling over a running daemon — every upgrade — used to fail with
// "Bootstrap failed: 5": bootout returns before launchd lets go of the
// label, and the bootstrap right behind it hit the old job. The command
// then left no daemon running and exited 0.
func TestApplyLaunchdWaitsForTheOldJobToUnload(t *testing.T) {
	f := &fakeLaunchd{loaded: true, unloadAfter: 3}
	if err := applyLaunchd(f.run, noSleep, "gui/501", "gui/501/"+LaunchdLabel, "/p.plist", true, time.Second); err != nil {
		t.Fatalf("enable over a running daemon failed: %v\ncalls: %v", err, f.verbs())
	}
	if !f.loaded {
		t.Error("service not loaded after enable")
	}
	v := f.verbs()
	if i, j := slices.Index(v, "bootout"), slices.Index(v, "bootstrap"); i < 0 || j < i {
		t.Errorf("want bootout before bootstrap, got %v", v)
	}
	if !slices.Contains(v, "kickstart") {
		t.Errorf("never started the job: %v", v)
	}
}

func TestApplyLaunchdOnAFreshMachine(t *testing.T) {
	f := &fakeLaunchd{}
	if err := applyLaunchd(f.run, noSleep, "gui/501", "gui/501/"+LaunchdLabel, "/p.plist", true, time.Second); err != nil {
		t.Fatalf("first install failed: %v", err)
	}
	if !f.loaded {
		t.Error("service not loaded after a first install")
	}
}

// An old job that never lets go is reported as such, not handed to a
// bootstrap that fails with launchd's opaque error 5.
func TestApplyLaunchdReportsAnOldJobThatWillNotUnload(t *testing.T) {
	f := &fakeLaunchd{loaded: true, unloadAfter: 1 << 30}
	clock := time.Duration(0)
	sleep := func(d time.Duration) { clock += d }
	err := applyLaunchd(f.run, sleep, "gui/501", "gui/501/"+LaunchdLabel, "/p.plist", true, 2*time.Second)
	if err == nil {
		t.Fatal("want an error when the old job never unloads")
	}
	if !strings.Contains(err.Error(), "still") {
		t.Errorf("error should say the old daemon is still unloading: %v", err)
	}
	if slices.Contains(f.verbs(), "bootstrap") {
		t.Errorf("bootstrapped over a job still loaded: %v", f.verbs())
	}
	if clock < 2*time.Second {
		t.Errorf("gave up after %s, before the %s allowed", clock, 2*time.Second)
	}
}

// An upgrade leaves the unit unchanged and the job loaded. Replacing the
// process in place is all it needs, and never unloads the job, so the
// bootstrap race cannot happen at all.
func TestApplyLaunchdRestartsAnUnchangedLoadedJobInPlace(t *testing.T) {
	f := &fakeLaunchd{loaded: true, unloadAfter: 3}
	if err := applyLaunchd(f.run, noSleep, "gui/501", "gui/501/"+LaunchdLabel, "/p.plist", false, time.Second); err != nil {
		t.Fatalf("restart in place failed: %v", err)
	}
	if v := f.verbs(); !slices.Equal(v, []string{"kickstart"}) {
		t.Errorf("want only kickstart -k for an unchanged loaded unit, got %v", v)
	}
}

// An unchanged unit that is not loaded — the state a failed reinstall left
// behind — is loaded, not merely kicked.
func TestApplyLaunchdLoadsAnUnchangedUnitThatIsNotLoaded(t *testing.T) {
	f := &fakeLaunchd{}
	if err := applyLaunchd(f.run, noSleep, "gui/501", "gui/501/"+LaunchdLabel, "/p.plist", false, time.Second); err != nil {
		t.Fatalf("load failed: %v", err)
	}
	if !f.loaded {
		t.Error("an unloaded unit was left unloaded")
	}
}

// launchd can reject a bootstrap with error 5 for a moment after print has
// stopped seeing the label. That is retried, not reported.
func TestApplyLaunchdRetriesABootstrapLaunchdStillRefuses(t *testing.T) {
	f := &fakeLaunchd{loaded: true, unloadAfter: 1, refuseBootstraps: 2}
	if err := applyLaunchd(f.run, noSleep, "gui/501", "gui/501/"+LaunchdLabel, "/p.plist", true, time.Second); err != nil {
		t.Fatalf("gave up on a bootstrap launchd accepts on the third try: %v", err)
	}
	if !f.loaded {
		t.Error("service not loaded")
	}
}
