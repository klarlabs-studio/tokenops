package menubarapp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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
