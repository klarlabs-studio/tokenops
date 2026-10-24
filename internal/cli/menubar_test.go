package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
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
	if err := installMenubar(&out, src, dst, true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dst); !os.IsNotExist(err) {
		t.Error("refresh installed an app that was never installed")
	}
	err := installMenubar(&out, "", dst, false)
	if err == nil || !strings.Contains(err.Error(), "Homebrew") {
		t.Errorf("missing app: %v", err)
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
