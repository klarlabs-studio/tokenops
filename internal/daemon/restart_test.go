package daemon

import (
	"slices"
	"strings"
	"testing"
)

// The argv is separated from running it so the command a supervisor is
// actually handed can be asserted. A restart that silently starts a second
// daemon, or loads a stale unit, is worse than telling the operator to do it
// themselves.
func TestRestartArgvPerSupervisor(t *testing.T) {
	launchd, err := restartArgv(UnitLaunchd)
	if err != nil {
		t.Fatalf("launchd: %v", err)
	}
	if launchd[0] != "launchctl" {
		t.Errorf("want launchctl, got %v", launchd)
	}
	// kickstart -k is the one launchd verb that stops a running job and
	// starts it again. Plain kickstart on a live job is a no-op, so the
	// command would report success and change nothing.
	if !slices.Contains(launchd, "kickstart") || !slices.Contains(launchd, "-k") {
		t.Errorf("want kickstart -k, got %v", launchd)
	}
	if !strings.HasSuffix(launchd[len(launchd)-1], LaunchdLabel) {
		t.Errorf("want the label as the target, got %v", launchd)
	}

	sysd, err := restartArgv(UnitSystemd)
	if err != nil {
		t.Fatalf("systemd: %v", err)
	}
	want := []string{"systemctl", "--user", "restart", SystemdName}
	if !slices.Equal(sysd, want) {
		t.Errorf("systemd argv = %v, want %v", sysd, want)
	}
}

func TestRestartArgvRejectsAnUnknownSupervisor(t *testing.T) {
	if _, err := restartArgv(UnitKind("initd")); err == nil {
		t.Fatal("want an error for an unknown supervisor")
	}
}
