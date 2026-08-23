package daemon

import (
	"strings"
	"testing"
)

// The launchd unit redirects stdout to ~/Library/Logs, so that — not
// ~/.tokenops/daemon.log — is where a supervised daemon's output lands.
func TestLogPathLaunchd(t *testing.T) {
	got := LogPath(UnitLaunchd, "/Users/x")
	if got != "/Users/x/Library/Logs/tokenops.log" {
		t.Errorf("LogPath = %q", got)
	}
	if strings.Contains(got, ".tokenops/daemon.log") {
		t.Error("must not point at the MCP-spawn log, which a supervised daemon never writes")
	}
}

// systemd has no log file to name; the journal is the answer.
func TestLogPathSystemd(t *testing.T) {
	if got := LogPath(UnitSystemd, "/home/x"); !strings.Contains(got, "journalctl") {
		t.Errorf("LogPath = %q, want the journal command", got)
	}
}

// An unknown supervisor yields nothing rather than a plausible-looking
// path nobody writes to.
func TestLogPathUnknownKind(t *testing.T) {
	if got := LogPath(UnitKind("cron"), "/home/x"); got != "" {
		t.Errorf("LogPath = %q, want empty for an unknown supervisor", got)
	}
}
