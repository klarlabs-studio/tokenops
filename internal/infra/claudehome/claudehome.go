// Package claudehome resolves where Claude Code keeps its files: the
// configuration directory (CLAUDE_CONFIG_DIR, else ~/.claude) and the
// settings files that Claude Code reads, highest precedence first.
//
// Every TokenOps reader that touches a Claude Code file goes through this
// package, so one override moves them all.
package claudehome

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

// EnvVar is the variable Claude Code uses to move its configuration
// directory.
const EnvVar = "CLAUDE_CONFIG_DIR"

// ManagedDir is the system directory holding managed-settings.json and its
// managed-settings.d/ drop-ins. It is a variable so tests can point it at
// a fixture; production code leaves it alone.
var ManagedDir = defaultManagedDir()

func defaultManagedDir() string {
	switch runtime.GOOS {
	case "darwin":
		return "/Library/Application Support/ClaudeCode"
	case "windows":
		pf := os.Getenv("ProgramFiles")
		if pf == "" {
			pf = `C:\Program Files`
		}
		return filepath.Join(pf, "ClaudeCode")
	default:
		return "/etc/claude-code"
	}
}

// Dir is the Claude Code configuration directory for this process. The
// first of these that is set wins: the environment (CLAUDE_CONFIG_DIR), the
// env block of the managed settings, the env block of the user's
// settings.json, and finally ~/.claude. The settings files are read only
// when the variable is not in the environment: the daemon does not share
// the shell Claude Code was started from.
func Dir() string {
	home, _ := os.UserHomeDir()
	return Resolve(os.Getenv, home)
}

// Resolve is Dir with the environment and the home directory passed in, for
// callers that already receive a home (or an environment) and must not read
// the process's own. An empty home falls back to a relative ".claude".
func Resolve(getenv func(string) string, home string) string {
	if d := getenv(EnvVar); d != "" {
		return d
	}
	for _, f := range managedFiles() {
		if d := envValue(f, EnvVar); d != "" {
			return d
		}
	}
	def := defaultDir(home)
	if d := envValue(filepath.Join(def, "settings.json"), EnvVar); d != "" {
		return d
	}
	return def
}

// Join is filepath.Join under Dir.
func Join(elem ...string) string {
	return filepath.Join(append([]string{Dir()}, elem...)...)
}

// SettingsFiles lists the settings files Claude Code reads that TokenOps
// may read, highest precedence first: managed settings (drop-ins before
// managed-settings.json, because Claude Code merges the drop-ins after it),
// then the user's settings.json in Dir. Files that do not exist are
// included; callers skip them.
func SettingsFiles() []string {
	return append(managedFiles(), Join("settings.json"))
}

// managedFiles lists the managed settings files, highest precedence first.
// Claude Code merges managed-settings.json first and then each *.json
// drop-in in alphabetical order, so a later file overrides an earlier one.
func managedFiles() []string {
	var out []string
	drops, _ := filepath.Glob(filepath.Join(ManagedDir, "managed-settings.d", "*.json"))
	sort.Strings(drops)
	for i := len(drops) - 1; i >= 0; i-- {
		if name := filepath.Base(drops[i]); strings.HasPrefix(name, ".") {
			continue
		}
		if fi, err := os.Stat(drops[i]); err == nil && !fi.IsDir() {
			out = append(out, drops[i])
		}
	}
	return append(out, filepath.Join(ManagedDir, "managed-settings.json"))
}

func defaultDir(home string) string {
	if home == "" {
		return ".claude"
	}
	return filepath.Join(home, ".claude")
}

// envValue reads env.<key> from one settings file; "" when the file is
// missing, unreadable or does not set it.
func envValue(path, key string) string {
	b, err := os.ReadFile(path) //nolint:gosec // fixed Claude Code settings paths
	if err != nil {
		return ""
	}
	var s struct {
		Env map[string]string `json:"env"`
	}
	if json.Unmarshal(b, &s) != nil {
		return ""
	}
	return s.Env[key]
}
