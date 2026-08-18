package daemon

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
)

// EnableUnit loads and starts the written unit. Launchd uses the modern
// bootstrap/kickstart path; systemd uses a user unit.
func EnableUnit(kind UnitKind, path string) error {
	switch kind {
	case UnitLaunchd:
		uid := strconv.Itoa(os.Getuid())
		domain := "gui/" + uid
		label := domain + "/" + LaunchdLabel
		// bootout is best-effort: first install has nothing to unload.
		_ = exec.Command("launchctl", "bootout", label).Run()
		if out, err := exec.Command("launchctl", "bootstrap", domain, path).CombinedOutput(); err != nil {
			return fmt.Errorf("launchctl bootstrap: %w (%s)", err, bytesPreview(out))
		}
		if out, err := exec.Command("launchctl", "enable", label).CombinedOutput(); err != nil {
			return fmt.Errorf("launchctl enable: %w (%s)", err, bytesPreview(out))
		}
		if out, err := exec.Command("launchctl", "kickstart", "-k", label).CombinedOutput(); err != nil {
			return fmt.Errorf("launchctl kickstart: %w (%s)", err, bytesPreview(out))
		}
		return nil
	case UnitSystemd:
		if out, err := exec.Command("systemctl", "--user", "daemon-reload").CombinedOutput(); err != nil {
			return fmt.Errorf("systemctl daemon-reload: %w (%s)", err, bytesPreview(out))
		}
		if out, err := exec.Command("systemctl", "--user", "enable", "--now", SystemdName).CombinedOutput(); err != nil {
			return fmt.Errorf("systemctl enable --now: %w (%s)", err, bytesPreview(out))
		}
		return nil
	default:
		return fmt.Errorf("supervisor: cannot load kind %q", kind)
	}
}

// DisableUnit unloads the unit. Missing/not-loaded is success.
func DisableUnit(kind UnitKind, path string) error {
	_ = path
	switch kind {
	case UnitLaunchd:
		uid := strconv.Itoa(os.Getuid())
		label := "gui/" + uid + "/" + LaunchdLabel
		if out, err := exec.Command("launchctl", "bootout", label).CombinedOutput(); err != nil {
			// launchctl bootout exits non-zero when the service is not loaded.
			_ = out
		}
		return nil
	case UnitSystemd:
		_ = exec.Command("systemctl", "--user", "disable", "--now", SystemdName).Run()
		return nil
	default:
		return fmt.Errorf("supervisor: cannot unload kind %q", kind)
	}
}

func bytesPreview(b []byte) string {
	s := string(b)
	if len(s) > 240 {
		return s[:240] + "…"
	}
	return s
}
