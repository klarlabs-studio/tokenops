package daemon

import (
	"bytes"
	_ "embed"
	"encoding/xml"
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

// UnitSpec is what a unit is rendered from.
type UnitSpec struct {
	// Bin is the tokenops binary the unit starts, Home the home directory
	// the daemon runs with.
	Bin, Home string
	// Config is the config file the daemon is started with (--config).
	// Empty leaves it to the daemon's default: tokenops/config.yaml in
	// ConfigHome, or in Home's .config.
	Config string
	// ConfigHome is the XDG_CONFIG_HOME the daemon runs with, for an
	// operator whose config directory is not Home's .config. A supervisor
	// starts the daemon without the shell's environment, so a daemon whose
	// unit does not carry it reads another config than the one the CLI
	// writes.
	ConfigHome string
}

// RenderUnit fills the launchd/systemd template from spec. An empty binary
// or home is an error so a placeholder never reaches disk.
//
// A spec with no Config and no ConfigHome renders the unit as it was
// before either existed, so an installed unit only changes for an
// operator who pins one.
func RenderUnit(kind UnitKind, spec UnitSpec) (string, error) {
	if spec.Bin == "" || strings.Contains(spec.Bin, "__TOKENOPS_BIN__") {
		return "", fmt.Errorf("supervisor: binary path is empty")
	}
	if spec.Home == "" || strings.Contains(spec.Home, "__HOME__") {
		return "", fmt.Errorf("supervisor: home directory is empty")
	}
	for name, v := range map[string]string{"config file": spec.Config, "config directory": spec.ConfigHome} {
		if v == "" {
			continue
		}
		if !filepath.IsAbs(v) || strings.ContainsAny(v, "\n\r\x00") || strings.Contains(v, "__TOKENOPS_") {
			return "", fmt.Errorf("supervisor: the %s %q is not a path a unit can carry; give it in full", name, v)
		}
	}
	tmpl, err := unitTemplate(kind)
	if err != nil {
		return "", err
	}
	args, env := pinnedConfig(kind, spec)
	body := strings.NewReplacer(
		"__TOKENOPS_BIN__", spec.Bin,
		"__TOKENOPS_CONFIG_ARGS__", args,
		"__TOKENOPS_CONFIG_ENV__", env,
		"__HOME__", spec.Home,
	).Replace(tmpl)
	if strings.Contains(body, "__TOKENOPS_") || strings.Contains(body, "__HOME__") {
		return "", fmt.Errorf("supervisor: unfilled placeholder in %s unit", kind)
	}
	return body, nil
}

// pinnedConfig is what the unit adds to the daemon's arguments and to its
// environment for the config spec pins; both empty when it pins none.
func pinnedConfig(kind UnitKind, spec UnitSpec) (args, env string) {
	switch kind {
	case UnitLaunchd:
		if spec.Config != "" {
			args = "\n    <string>--config</string>\n    <string>" + xmlText(spec.Config) + "</string>"
		}
		if spec.ConfigHome != "" {
			env = "\n    <key>XDG_CONFIG_HOME</key>\n    <string>" + xmlText(spec.ConfigHome) + "</string>"
		}
	case UnitSystemd:
		if spec.Config != "" {
			args = " --config " + systemdQuote(spec.Config, true)
		}
		if spec.ConfigHome != "" {
			env = "\nEnvironment=" + systemdQuote("XDG_CONFIG_HOME="+spec.ConfigHome, false)
		}
	}
	return args, env
}

func xmlText(s string) string {
	var b strings.Builder
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}

// systemdQuote writes s as one quoted word of a unit file. systemd expands
// % everywhere, and $ in a command line.
func systemdQuote(s string, command bool) string {
	pairs := []string{`\`, `\\`, `"`, `\"`, `%`, `%%`}
	if command {
		pairs = append(pairs, `$`, `$$`)
	}
	return `"` + strings.NewReplacer(pairs...).Replace(s) + `"`
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
