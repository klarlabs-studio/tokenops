package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"go.klarlabs.de/tokenops/internal/infra/menubarapp"
)

// An upgrade's refresh never installs an app nobody asked for, and a
// binary without the app beside it says where the app comes from.
func TestInstallMenubarRefreshAndMissing(t *testing.T) {
	dir := t.TempDir()
	dst := filepath.Join(dir, "Applications", menubarApp)
	src := filepath.Join(dir, "shipped", menubarApp)
	if err := os.MkdirAll(filepath.Join(src, "Contents"), 0o755); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := installMenubar(&out, src, dst, true, inertOps(t)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dst); !os.IsNotExist(err) {
		t.Error("refresh installed an app that was never installed")
	}
	err := installMenubar(&out, "", dst, false, inertOps(t))
	if err == nil || !strings.Contains(err.Error(), "Homebrew") {
		t.Errorf("missing app: %v", err)
	}
}

// inertOps fails the test on any side effect: these cases must decide
// before touching the app.
func inertOps(t *testing.T) menubarapp.Ops {
	t.Helper()
	return menubarapp.Ops{
		Running: func() bool { return false },
		Wanted:  func() bool { return false },
		Quit:    func() { t.Error("quit the app") },
		Replace: func(string, string) error { t.Error("copied the app"); return nil },
		Open:    func(string) error { t.Error("opened the app"); return nil },
	}
}

// The refresh stops the app's own process and nothing else in its bundle:
// the presence helper, TokenOps.app/Contents/MacOS/tokenops-presence,
// may be mid-signature with the person's dialog up, and killing it would
// cancel an approval they are giving.
func TestMenubarProcessPatternMatchesOnlyTheApp(t *testing.T) {
	re := regexp.MustCompile(menubarProcess) // pkill -f and pgrep -f take an extended regex; this one reads the same in RE2
	for _, cmdline := range []string{
		"/Users/x/Applications/TokenOps.app/Contents/MacOS/TokenOps",
		"/Applications/TokenOps.app/Contents/MacOS/TokenOps -psn_0_12345",
	} {
		if !re.MatchString(cmdline) {
			t.Errorf("does not match the app: %q", cmdline)
		}
	}
	for _, cmdline := range []string{
		"/Users/x/Applications/TokenOps.app/Contents/MacOS/tokenops-presence sign --blob /Users/x/.tokenops/presence/key.blob " +
			`--reason approve TokenOps boundary "session-spend" (continue) in claude-code session s1`,
		"/Applications/TokenOps.app/Contents/MacOS/tokenops-presence enroll --blob /tmp/key.blob.new",
		"/Users/x/Applications/TokenOps.app/Contents/MacOS/TokenOpsHelper",
		"/opt/homebrew/bin/tokenops menubar --refresh",
	} {
		if re.MatchString(cmdline) {
			t.Errorf("matches what is not the app: %q", cmdline)
		}
	}
}

// `tokenops menubar` on an installed, current app that is not running
// opens it without copying: the command always ends with the app open.
func TestInstallMenubarOpensAnInstalledAppThatIsNotRunning(t *testing.T) {
	dir := t.TempDir()
	for _, app := range []string{filepath.Join(dir, "shipped", menubarApp), filepath.Join(dir, "Applications", menubarApp)} {
		if err := os.MkdirAll(filepath.Join(app, "Contents", "MacOS"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(app, "Contents", "MacOS", "TokenOps"), []byte("same"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	src, dst := filepath.Join(dir, "shipped", menubarApp), filepath.Join(dir, "Applications", menubarApp)
	opened := 0
	ops := inertOps(t)
	ops.Open = func(string) error { opened++; return nil }
	var out bytes.Buffer
	if err := installMenubar(&out, src, dst, false, ops); err != nil {
		t.Fatal(err)
	}
	if opened != 1 || strings.Contains(out.String(), "Installed") {
		t.Errorf("opened=%d out=%q", opened, out.String())
	}
}
