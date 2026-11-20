package menubarapp

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// bundle lays out a minimal app bundle: an Info.plist with the given
// version strings (empty means the key is omitted) and an executable with
// the given content.
func bundle(t *testing.T, root, short, build, exe string) string {
	t.Helper()
	app := filepath.Join(root, AppName)
	if err := os.MkdirAll(filepath.Join(app, "Contents", "MacOS"), 0o755); err != nil {
		t.Fatal(err)
	}
	var plist strings.Builder
	plist.WriteString(`<?xml version="1.0" encoding="UTF-8"?><plist version="1.0"><dict>`)
	if short != "" {
		plist.WriteString("<key>CFBundleShortVersionString</key><string>" + short + "</string>")
	}
	if build != "" {
		plist.WriteString("<key>CFBundleVersion</key><string>" + build + "</string>")
	}
	plist.WriteString(`</dict></plist>`)
	if err := os.WriteFile(filepath.Join(app, "Contents", "Info.plist"), []byte(plist.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(app, "Contents", "MacOS", "TokenOps"), []byte(exe), 0o755); err != nil {
		t.Fatal(err)
	}
	return app
}

func TestDiffersByVersion(t *testing.T) {
	root := t.TempDir()
	shipped := bundle(t, filepath.Join(root, "s"), "0.66.0", "1", "same")
	installed := bundle(t, filepath.Join(root, "i"), "0.65.0", "1", "same")
	got, err := Differs(shipped, installed)
	if err != nil || !got {
		t.Fatalf("different versions: differs=%v err=%v", got, err)
	}
}

// A same-version rebuild (a dev build, or a fix shipped without a bump)
// still has different bytes, so the executable's content decides.
func TestDiffersByExecutableContentWhenVersionsMatch(t *testing.T) {
	root := t.TempDir()
	shipped := bundle(t, filepath.Join(root, "s"), "0.66.0", "1", "new bytes")
	installed := bundle(t, filepath.Join(root, "i"), "0.66.0", "1", "old bytes")
	if got, err := Differs(shipped, installed); err != nil || !got {
		t.Fatalf("same version, different bytes: differs=%v err=%v", got, err)
	}
}

func TestIdenticalBundlesDoNotDiffer(t *testing.T) {
	root := t.TempDir()
	shipped := bundle(t, filepath.Join(root, "s"), "0.66.0", "1", "same")
	installed := bundle(t, filepath.Join(root, "i"), "0.66.0", "1", "same")
	if got, err := Differs(shipped, installed); err != nil || got {
		t.Fatalf("identical bundles: differs=%v err=%v", got, err)
	}
}

// Without version keys the executable alone decides.
func TestDiffersWithoutVersionKeysUsesExecutable(t *testing.T) {
	root := t.TempDir()
	same := bundle(t, filepath.Join(root, "s"), "", "", "same")
	other := bundle(t, filepath.Join(root, "i"), "", "", "same")
	if got, err := Differs(same, other); err != nil || got {
		t.Fatalf("no versions, same bytes: differs=%v err=%v", got, err)
	}
	changed := bundle(t, filepath.Join(root, "c"), "", "", "changed")
	if got, err := Differs(same, changed); err != nil || !got {
		t.Fatalf("no versions, different bytes: differs=%v err=%v", got, err)
	}
}

// An installed copy whose executable is gone is broken; a refresh fixes it.
func TestDiffersWhenInstalledExecutableIsMissing(t *testing.T) {
	root := t.TempDir()
	shipped := bundle(t, filepath.Join(root, "s"), "0.66.0", "1", "x")
	installed := bundle(t, filepath.Join(root, "i"), "0.66.0", "1", "x")
	if err := os.Remove(filepath.Join(installed, "Contents", "MacOS", "TokenOps")); err != nil {
		t.Fatal(err)
	}
	if got, err := Differs(shipped, installed); err != nil || !got {
		t.Fatalf("broken install: differs=%v err=%v", got, err)
	}
}

// fakeOps records what a refresh did instead of touching launchd or the
// real ~/Applications.
type fakeOps struct {
	running bool
	quits   int
	copies  []string
	opened  []string
	copyErr error
}

func (f *fakeOps) ops() Ops {
	return Ops{
		Running: func() bool { return f.running },
		Quit:    func() { f.quits++ },
		Replace: func(src, dst string) error {
			f.copies = append(f.copies, src+" -> "+dst)
			return f.copyErr
		},
		Open: func(dst string) error { f.opened = append(f.opened, dst); return nil },
	}
}

// The incident this guards against: an upgrade left the menu bar app at the
// old version. The refresh replaces it and reopens it because it was open.
func TestRefreshReplacesDifferingAppAndRelaunchesWhenRunning(t *testing.T) {
	root := t.TempDir()
	shipped := bundle(t, filepath.Join(root, "s"), "0.66.0", "1", "new")
	installed := bundle(t, filepath.Join(root, "Applications"), "0.65.0", "1", "old")
	f := &fakeOps{running: true}
	res, err := Refresh(shipped, installed, f.ops())
	if err != nil {
		t.Fatal(err)
	}
	if !res.Refreshed || !res.Relaunched {
		t.Fatalf("result = %+v, want refreshed and relaunched", res)
	}
	if f.quits != 1 || len(f.copies) != 1 || len(f.opened) != 1 || f.opened[0] != installed {
		t.Errorf("quit=%d copies=%v opened=%v", f.quits, f.copies, f.opened)
	}
}

// A refresh of an app that was closed installs the new copy and leaves it
// closed: it must not pop a window open on every upgrade or restart.
func TestRefreshDoesNotLaunchAppThatWasNotRunning(t *testing.T) {
	root := t.TempDir()
	shipped := bundle(t, filepath.Join(root, "s"), "0.66.0", "1", "new")
	installed := bundle(t, filepath.Join(root, "Applications"), "0.65.0", "1", "old")
	f := &fakeOps{running: false}
	res, err := Refresh(shipped, installed, f.ops())
	if err != nil {
		t.Fatal(err)
	}
	if !res.Refreshed || res.Relaunched {
		t.Fatalf("result = %+v, want refreshed and not relaunched", res)
	}
	if f.quits != 0 || len(f.copies) != 1 || len(f.opened) != 0 {
		t.Errorf("quit=%d copies=%v opened=%v", f.quits, f.copies, f.opened)
	}
}

// A plain daemon restart with nothing new shipped must not touch the app:
// no copy, no quit, no relaunch.
func TestRefreshIsNoOpWhenShippedEqualsInstalled(t *testing.T) {
	root := t.TempDir()
	shipped := bundle(t, filepath.Join(root, "s"), "0.66.0", "1", "same")
	installed := bundle(t, filepath.Join(root, "Applications"), "0.66.0", "1", "same")
	f := &fakeOps{running: true}
	res, err := Refresh(shipped, installed, f.ops())
	if err != nil {
		t.Fatal(err)
	}
	if res.Refreshed || res.Relaunched || f.quits != 0 || len(f.copies) != 0 || len(f.opened) != 0 {
		t.Fatalf("equal app was touched: result=%+v quit=%d copies=%v opened=%v", res, f.quits, f.copies, f.opened)
	}
}

// Never installed: a daemon start must not install an app nobody asked for.
func TestRefreshNeverInstallsAnAbsentApp(t *testing.T) {
	root := t.TempDir()
	shipped := bundle(t, filepath.Join(root, "s"), "0.66.0", "1", "new")
	absent := filepath.Join(root, "Applications", AppName)
	f := &fakeOps{running: true}
	res, err := Refresh(shipped, absent, f.ops())
	if err != nil {
		t.Fatal(err)
	}
	if res.Refreshed || len(f.copies) != 0 || len(f.opened) != 0 {
		t.Fatalf("absent app was installed: result=%+v copies=%v", res, f.copies)
	}
	if _, err := os.Stat(absent); !os.IsNotExist(err) {
		t.Error("absent app now exists on disk")
	}
}

// No shipped bundle beside the binary (a source build) changes nothing.
func TestRefreshWithoutShippedBundleIsNoOp(t *testing.T) {
	root := t.TempDir()
	installed := bundle(t, filepath.Join(root, "Applications"), "0.65.0", "1", "old")
	f := &fakeOps{running: true}
	res, err := Refresh("", installed, f.ops())
	if err != nil || res.Refreshed || len(f.copies) != 0 {
		t.Fatalf("no shipped app: result=%+v err=%v copies=%v", res, err, f.copies)
	}
}

// A failed copy is reported and never claims a refresh. The app the
// refresh quit is reopened when its old copy is still in place, so a failed
// upgrade does not leave the menu bar empty; the daemon keeps running.
func TestRefreshCopyFailureReopensTheOldAppAndIsReported(t *testing.T) {
	root := t.TempDir()
	shipped := bundle(t, filepath.Join(root, "s"), "0.66.0", "1", "new")
	installed := bundle(t, filepath.Join(root, "Applications"), "0.65.0", "1", "old")
	f := &fakeOps{running: true, copyErr: os.ErrPermission}
	res, err := Refresh(shipped, installed, f.ops())
	if err == nil {
		t.Fatal("copy failure not reported")
	}
	if res.Refreshed || !res.Relaunched || len(f.opened) != 1 || f.opened[0] != installed {
		t.Errorf("after failed copy: result=%+v opened=%v, want the old app reopened", res, f.opened)
	}
}

// When the failed copy took the old app with it, there is nothing to reopen.
func TestRefreshCopyFailureWithAppGoneOpensNothing(t *testing.T) {
	root := t.TempDir()
	shipped := bundle(t, filepath.Join(root, "s"), "0.66.0", "1", "new")
	installed := bundle(t, filepath.Join(root, "Applications"), "0.65.0", "1", "old")
	f := &fakeOps{running: true}
	ops := f.ops()
	ops.Replace = func(src, dst string) error {
		_ = os.RemoveAll(dst)
		return os.ErrPermission
	}
	res, err := Refresh(shipped, installed, ops)
	if err == nil || res.Refreshed || res.Relaunched || len(f.opened) != 0 {
		t.Errorf("result=%+v err=%v opened=%v, want error and nothing opened", res, err, f.opened)
	}
}

func TestShippedBesideResolvesSymlinkedBinary(t *testing.T) {
	root := t.TempDir()
	caskDir := filepath.Join(root, "Caskroom", "tokenops", "0.66.0")
	bundle(t, caskDir, "0.66.0", "1", "x")
	bin := filepath.Join(caskDir, "tokenops")
	if err := os.WriteFile(bin, []byte("bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "bin", "tokenops")
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(bin, link); err != nil {
		t.Fatal(err)
	}
	got := ShippedBeside(link)
	if got == "" || filepath.Base(got) != AppName {
		t.Fatalf("ShippedBeside(%s) = %q", link, got)
	}
	if ShippedBeside(filepath.Join(root, "nowhere", "tokenops")) != "" {
		t.Error("a binary with no bundle beside it reported one")
	}
}

// writeIntent writes the app's preferences file with the given JSON.
func writeIntent(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "TokenOps", "menubar.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// The app records whether the person keeps it in the menu bar; the daemon
// reads that back. Only an explicit yes counts: a missing, unreadable or
// older file (no such keys) is not a request to open anything.
func TestWantedReadsTheAppsIntent(t *testing.T) {
	tests := []struct {
		name string
		body string
		want bool
	}{
		{"left open", `{"open":true}`, true},
		{"quit from its menu", `{"open":false}`, false},
		{"quit, but launch at login on", `{"open":false,"launch_at_login":true}`, true},
		{"launch at login on, open unknown", `{"launch_at_login":true}`, true},
		{"an older app that records neither", `{"ring":"anthropic"}`, false},
		{"garbage", `not json`, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := Wanted(writeIntent(t, tc.body)); got != tc.want {
				t.Errorf("Wanted = %v, want %v", got, tc.want)
			}
		})
	}
	if Wanted(filepath.Join(t.TempDir(), "absent.json")) {
		t.Error("a missing file asked for the app")
	}
}

// The incident: after an upgrade the app was gone from the menu bar,
// because a refresh reopened it only when it happened to be running at the
// moment the new daemon started. Whether the person keeps it in the menu
// bar decides as well, so an app that was not running then (it had exited,
// or macOS had not started it after a reboot) comes back on the new version.
func TestRefreshReopensWhatThePersonKeepsInTheMenuBar(t *testing.T) {
	tests := []struct {
		name         string
		running      bool
		wanted       bool
		wantQuit     int
		wantOpened   int
		wantReopened bool
	}{
		{"running and wanted", true, true, 1, 1, true},
		{"running, intent unknown", true, false, 1, 1, true},
		{"not running but kept in the menu bar", false, true, 0, 1, true},
		{"not running and quit by the person", false, false, 0, 0, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			shipped := bundle(t, filepath.Join(root, "s"), "0.67.0", "1", "new")
			installed := bundle(t, filepath.Join(root, "Applications"), "0.66.0", "1", "old")
			f := &fakeOps{running: tc.running}
			ops := f.ops()
			ops.Wanted = func() bool { return tc.wanted }
			res, err := Refresh(shipped, installed, ops)
			if err != nil {
				t.Fatal(err)
			}
			if !res.Refreshed || res.Relaunched != tc.wantReopened || f.quits != tc.wantQuit || len(f.opened) != tc.wantOpened {
				t.Errorf("result=%+v quits=%d opened=%v", res, f.quits, f.opened)
			}
		})
	}
}

// `tokenops menubar` always ends with the app open: it installs or
// replaces the copy only when that is needed, never quits an app that is
// already the shipped one, and opens it when it is not running.
func TestInstallEndsWithTheAppOpen(t *testing.T) {
	tests := []struct {
		name          string
		installed     string // "" means not installed
		shipped       bool
		running       bool
		wantCopies    int
		wantQuits     int
		wantOpened    int
		wantRefreshed bool
	}{
		{"first install", "", true, false, 1, 0, 1, true},
		{"installed, current, not running", "new", true, false, 0, 0, 1, false},
		{"installed, current, running", "new", true, true, 0, 0, 0, false},
		{"installed, stale, running", "old", true, true, 1, 1, 1, true},
		{"installed, stale, not running", "old", true, false, 1, 0, 1, true},
		{"installed, nothing shipped (source build), not running", "old", false, false, 0, 0, 1, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			src := ""
			if tc.shipped {
				src = bundle(t, filepath.Join(root, "s"), "0.67.0", "1", "new")
			}
			dst := filepath.Join(root, "Applications", AppName)
			if tc.installed != "" {
				v := "0.67.0"
				if tc.installed == "old" {
					v = "0.66.0"
				}
				dst = bundle(t, filepath.Join(root, "Applications"), v, "1", tc.installed)
			}
			f := &fakeOps{running: tc.running}
			ops := f.ops()
			// A quit stops the app; an open starts it.
			ops.Quit = func() { f.quits++; f.running = false }
			ops.Open = func(p string) error { f.opened = append(f.opened, p); f.running = true; return nil }
			res, err := Install(src, dst, ops)
			if err != nil {
				t.Fatal(err)
			}
			if len(f.copies) != tc.wantCopies || f.quits != tc.wantQuits || len(f.opened) != tc.wantOpened || res.Refreshed != tc.wantRefreshed {
				t.Errorf("result=%+v copies=%v quits=%d opened=%v", res, f.copies, f.quits, f.opened)
			}
			if !f.running {
				t.Error("the app is not open at the end")
			}
		})
	}
}

func TestInstallWithoutAnyAppSaysWhereItComesFrom(t *testing.T) {
	f := &fakeOps{}
	_, err := Install("", filepath.Join(t.TempDir(), AppName), f.ops())
	if !errors.Is(err, ErrNotShipped) {
		t.Fatalf("err = %v, want ErrNotShipped", err)
	}
}

// An open is confirmed: `open` can return before the app is up, or hand
// the request to an instance that is still exiting. The launch waits for
// the process and asks once more before it reports failure.
func TestLaunchConfirmsTheAppRuns(t *testing.T) {
	tests := []struct {
		name      string
		upAfter   int // the app is seen running from this check on; -1 never
		openErr   error
		wantOpens int
		wantErr   bool
	}{
		{"up at once", 0, nil, 1, false},
		{"up after a few checks", 3, nil, 1, false},
		{"first open lost, second works", launchChecks + 1, nil, 2, false},
		{"never starts", -1, nil, 2, true},
		{"open fails", -1, os.ErrPermission, 1, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			opens, checks := 0, 0
			err := launch(
				func() error { opens++; return tc.openErr },
				func() bool { checks++; return tc.upAfter >= 0 && checks > tc.upAfter },
				func(time.Duration) {},
			)
			if (err != nil) != tc.wantErr || opens != tc.wantOpens {
				t.Errorf("err=%v opens=%d", err, opens)
			}
		})
	}
}

// A quit waits for the process to be gone, so a reopen right after it is
// not handed to the exiting instance; one that ignores the request is
// stopped hard.
func TestStopWaitsForTheAppToExit(t *testing.T) {
	tests := []struct {
		name      string
		goneAfter int // checks until gone; -1 only after the hard stop
		wantHard  bool
	}{
		{"exits at once", 0, false},
		{"exits after a moment", 4, false},
		{"ignores the request", -1, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			checks, hard := 0, false
			stop(
				func(force bool) { hard = hard || force },
				func() bool {
					checks++
					if hard {
						return false
					}
					return tc.goneAfter < 0 || checks <= tc.goneAfter
				},
				func(time.Duration) {},
			)
			if hard != tc.wantHard {
				t.Errorf("hard stop = %v, want %v", hard, tc.wantHard)
			}
		})
	}
}
