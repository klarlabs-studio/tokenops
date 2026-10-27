package daemon

import (
	"os"
	"path/filepath"
	"testing"

	"go.klarlabs.de/tokenops/internal/infra/menubarapp"
)

// The daemon refreshes the installed menu bar app with the same rules as
// `tokenops menubar --refresh`. These tests inject the process operations
// and use temp dirs, so the real ~/Applications and TokenOps.app stay alone.
type menubarFake struct {
	running bool
	quits   int
	copied  int
	opened  int
}

func (f *menubarFake) ops() menubarapp.Ops {
	return menubarapp.Ops{
		Running: func() bool { return f.running },
		Quit:    func() { f.quits++ },
		Replace: func(string, string) error { f.copied++; return nil },
		Open:    func(string) error { f.opened++; return nil },
	}
}

// writeApp writes a minimal bundle at dir/TokenOps.app with the given
// executable bytes.
func writeApp(t *testing.T, dir, exe string) string {
	t.Helper()
	app := filepath.Join(dir, menubarapp.AppName)
	if err := os.MkdirAll(filepath.Join(app, "Contents", "MacOS"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(app, "Contents", "MacOS", "TokenOps"), []byte(exe), 0o755); err != nil {
		t.Fatal(err)
	}
	return app
}

// A daemon start after a plain restart, with the app unchanged, does nothing.
func TestMenubarRefreshAtStartIsNoOpWhenIdentical(t *testing.T) {
	root := t.TempDir()
	shipped := writeApp(t, filepath.Join(root, "shipped"), "same")
	installed := writeApp(t, filepath.Join(root, "Applications"), "same")
	f := &menubarFake{running: true}
	res := refreshMenubarAt(shipped, installed, f.ops(), discardLogger())
	if res.Refreshed || f.quits != 0 || f.copied != 0 || f.opened != 0 {
		t.Fatalf("identical app touched: %+v quits=%d copied=%d opened=%d", res, f.quits, f.copied, f.opened)
	}
}

// An upgrade leaves the installed app behind; the first start on the new
// binary replaces it and reopens it because it was open.
func TestMenubarRefreshAtStartReplacesStaleAppAndReopensIt(t *testing.T) {
	root := t.TempDir()
	shipped := writeApp(t, filepath.Join(root, "shipped"), "new build")
	installed := writeApp(t, filepath.Join(root, "Applications"), "old build")
	f := &menubarFake{running: true}
	res := refreshMenubarAt(shipped, installed, f.ops(), discardLogger())
	if !res.Refreshed || !res.Relaunched || f.copied != 1 || f.opened != 1 {
		t.Fatalf("stale app: %+v copied=%d opened=%d", res, f.copied, f.opened)
	}
}

// Never installed means the daemon does not install it.
func TestMenubarRefreshAtStartNeverInstalls(t *testing.T) {
	root := t.TempDir()
	shipped := writeApp(t, filepath.Join(root, "shipped"), "new build")
	absent := filepath.Join(root, "Applications", menubarapp.AppName)
	f := &menubarFake{running: true}
	res := refreshMenubarAt(shipped, absent, f.ops(), discardLogger())
	if res.Refreshed || f.copied != 0 {
		t.Fatalf("absent app installed: %+v copied=%d", res, f.copied)
	}
	if _, err := os.Stat(absent); !os.IsNotExist(err) {
		t.Error("absent app exists after refresh")
	}
}
