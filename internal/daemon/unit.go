package daemon

import (
	"bytes"
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Supervisor kinds `tokenops daemon install` can write. Detected from GOOS
// unless the operator passes --kind.
type UnitKind string

const (
	UnitLaunchd UnitKind = "launchd"
	UnitSystemd UnitKind = "systemd"
)

const (
	LaunchdLabel = "de.klarlabs.tokenops"
	SystemdName  = "tokenops.service"
)

//go:embed embed/launchd.plist
var launchdTemplate string

//go:embed embed/systemd.service
var systemdTemplate string

// DetectUnitKind returns the supervisor this OS can install, or an error
// on platforms with no installer (Windows). The operator can still run
// `tokenops start` in a persistent session.
func DetectUnitKind() (UnitKind, error) {
	switch runtime.GOOS {
	case "darwin":
		return UnitLaunchd, nil
	case "linux":
		return UnitSystemd, nil
	default:
		return "", fmt.Errorf("no supervisor installer for %s; run `tokenops start` in a persistent session", runtime.GOOS)
	}
}

// ParseUnitKind accepts launchd, systemd, or auto (empty).
func ParseUnitKind(s string) (UnitKind, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "auto":
		return DetectUnitKind()
	case string(UnitLaunchd):
		return UnitLaunchd, nil
	case string(UnitSystemd):
		return UnitSystemd, nil
	default:
		return "", fmt.Errorf("unknown supervisor kind %q (want launchd, systemd, or auto)", s)
	}
}

// DefaultUnitPath is where the unit file is written for a given kind.
func DefaultUnitPath(kind UnitKind, home string) string {
	switch kind {
	case UnitLaunchd:
		return filepath.Join(home, "Library", "LaunchAgents", LaunchdLabel+".plist")
	case UnitSystemd:
		return filepath.Join(home, ".config", "systemd", "user", SystemdName)
	default:
		return ""
	}
}

// RenderUnit fills the launchd/systemd template with the resolved binary
// path and home directory. Empty bin/home is an error so a placeholder
// never reaches disk.
func RenderUnit(kind UnitKind, bin, home string) (string, error) {
	if bin == "" || strings.Contains(bin, "__TOKENOPS_BIN__") {
		return "", fmt.Errorf("supervisor: binary path is empty")
	}
	if home == "" || strings.Contains(home, "__HOME__") {
		return "", fmt.Errorf("supervisor: home directory is empty")
	}
	tmpl, err := unitTemplate(kind)
	if err != nil {
		return "", err
	}
	body := strings.NewReplacer(
		"__TOKENOPS_BIN__", bin,
		"__HOME__", home,
	).Replace(tmpl)
	if strings.Contains(body, "__TOKENOPS_BIN__") || strings.Contains(body, "__HOME__") {
		return "", fmt.Errorf("supervisor: unfilled placeholder in %s unit", kind)
	}
	return body, nil
}

func unitTemplate(kind UnitKind) (string, error) {
	switch kind {
	case UnitLaunchd:
		return launchdTemplate, nil
	case UnitSystemd:
		return systemdTemplate, nil
	default:
		return "", fmt.Errorf("supervisor: unknown kind %q", kind)
	}
}

// WriteUnit writes the rendered unit atomically at path with mode 0600.
func WriteUnit(path, body string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("supervisor: mkdir %s: %w", filepath.Dir(path), err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(body), 0o600); err != nil {
		return fmt.Errorf("supervisor: write %s: %w", tmp, err)
	}
	if err := os.Chmod(tmp, 0o600); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("supervisor: chmod %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("supervisor: rename %s: %w", path, err)
	}
	return os.Chmod(path, 0o600)
}

// RemoveUnit deletes the unit file. Missing is success.
func RemoveUnit(path string) error {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("supervisor: remove %s: %w", path, err)
	}
	return nil
}

// UnitEqual reports whether two unit bodies are the same after trimming
// trailing whitespace, so a re-install of an identical unit is a no-op.
func UnitEqual(a, b string) bool {
	return bytes.Equal(bytes.TrimSpace([]byte(a)), bytes.TrimSpace([]byte(b)))
}
