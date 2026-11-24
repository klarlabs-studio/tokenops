package sourceprobe

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"go.klarlabs.de/tokenops/internal/contexts/spend/providers"
)

// Machine is where the presence probes look: the home directory, the
// environment and the application folders. Tests give a temp home.
type Machine struct {
	Home string
	// Getenv reads XDG_CONFIG_HOME and XDG_DATA_HOME.
	Getenv func(string) string
	// LookPath finds a binary on PATH.
	LookPath func(string) (string, error)
	// AppDirs are where application bundles are installed; only macOS
	// has them.
	AppDirs []string
}

// ThisMachine is the machine the process runs on. Its home is empty when
// it cannot be told, and then only binaries are looked for.
func ThisMachine() Machine {
	home, _ := os.UserHomeDir()
	m := Machine{Home: home, Getenv: os.Getenv, LookPath: exec.LookPath}
	if runtime.GOOS == "darwin" {
		m.AppDirs = []string{"/Applications"}
		if home != "" {
			m.AppDirs = append(m.AppDirs, filepath.Join(home, "Applications"))
		}
	}
	return m
}

// InstalledTools is, per provider, what of its tool is on m: an app
// bundle's path, a directory with ~ for the home, or "<binary> on PATH",
// from the registry's presence probes. It only asks whether each path
// exists: no file is opened, no credential read, and nothing it touches
// is behind a macOS privacy prompt (the registry keeps ~/Library out).
func InstalledTools(m Machine) map[string][]string {
	out := map[string][]string{}
	for _, d := range providers.All() {
		for _, p := range d.Presence {
			if found, ok := m.find(p); ok {
				out[string(d.ID)] = append(out[string(d.ID)], found)
			}
		}
	}
	return out
}

// find reports what of p is on m, in words.
func (m Machine) find(p providers.Presence) (string, bool) {
	switch {
	case p.App != "":
		for _, dir := range m.AppDirs {
			path := filepath.Join(dir, p.App)
			if isDir(path) {
				return m.tilde(path), true
			}
		}
	case p.Dir != "":
		if path := m.expand(p.Dir); path != "" && isDir(path) {
			return m.tilde(path), true
		}
	case p.Binary != "" && m.LookPath != nil:
		if _, err := m.LookPath(p.Binary); err == nil {
			return p.Binary + " on PATH", true
		}
	}
	return "", false
}

// expand resolves a registry directory on m; empty when it cannot be.
func (m Machine) expand(dir string) string {
	for prefix, env := range map[string]struct{ name, fallback string }{
		"$XDG_CONFIG_HOME/": {"XDG_CONFIG_HOME", ".config"},
		"$XDG_DATA_HOME/":   {"XDG_DATA_HOME", filepath.Join(".local", "share")},
	} {
		if rest, ok := strings.CutPrefix(dir, prefix); ok {
			base := ""
			if m.Getenv != nil {
				base = m.Getenv(env.name)
			}
			if base == "" && m.Home != "" {
				base = filepath.Join(m.Home, env.fallback)
			}
			if base == "" {
				return ""
			}
			return filepath.Join(base, rest)
		}
	}
	if rest, ok := strings.CutPrefix(dir, "~/"); ok && m.Home != "" {
		return filepath.Join(m.Home, rest)
	}
	return ""
}

// tilde shows a path under the home directory with ~, so evidence never
// carries the operator's user name.
func (m Machine) tilde(path string) string {
	if m.Home != "" {
		if rest, ok := strings.CutPrefix(path, m.Home+string(filepath.Separator)); ok {
			return "~/" + filepath.ToSlash(rest)
		}
	}
	return path
}

func isDir(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.IsDir()
}
