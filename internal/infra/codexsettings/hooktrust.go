package codexsettings

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"
)

// HookTrust reports whether Codex's hooks.json under home holds a command
// hook for event (its hooks.json key, "PreToolUse") whose command contains
// marker, and whether Codex has armed it: a user hook runs only once its
// definition is trusted in Codex's /hooks, which records a trusted_hash for
// its slot ("<hooks.json>:pre_tool_use:<group>:<hook>") in config.toml, and
// not while that slot is disabled. Codex skips an untrusted hook silently,
// so a hook that is only written must not be reported as working.
//
// The hash itself is not recomputed: trust is read by slot. A definition
// edited after it was trusted still reads as trusted here, while Codex
// asks again; re-installing through TokenOps writes the same definition.
func HookTrust(home, event, marker string) (installed, trusted bool) {
	path := filepath.Join(home, "hooks.json")
	b, err := os.ReadFile(path) //nolint:gosec // Codex's own hook config
	if err != nil {
		return false, false
	}
	var doc struct {
		Hooks map[string][]struct {
			Hooks []struct {
				Command string `json:"command"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if json.Unmarshal(b, &doc) != nil {
		return false, false
	}
	var slots []string
	for g, group := range doc.Hooks[event] {
		for h, hook := range group.Hooks {
			if strings.Contains(hook.Command, marker) {
				slots = append(slots, fmt.Sprintf("%s:%s:%d:%d", path, snakeCase(event), g, h))
			}
		}
	}
	if len(slots) == 0 {
		return false, false
	}
	states := hookStates(ConfigPathIn(home))
	for _, slot := range slots {
		if s := states[slot]; s.trusted && !s.disabled {
			return true, true
		}
	}
	return true, false
}

// ConfigPathIn is config.toml under a Codex home.
func ConfigPathIn(home string) string { return filepath.Join(home, "config.toml") }

type hookState struct{ trusted, disabled bool }

// hookStates reads config.toml's [hooks.state."<slot>"] tables, line by
// line like the rest of this package: each slot's trusted_hash and
// enabled = false.
func hookStates(configPath string) map[string]hookState {
	b, err := os.ReadFile(configPath) //nolint:gosec // Codex's own config path
	if err != nil {
		return nil
	}
	out := map[string]hookState{}
	slot := ""
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "[") {
			slot = ""
			if rest, ok := strings.CutPrefix(line, `[hooks.state."`); ok {
				if key, ok := strings.CutSuffix(rest, `"]`); ok {
					slot = key
				}
			}
			continue
		}
		if slot == "" {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		s := out[slot]
		switch strings.TrimSpace(key) {
		case "trusted_hash":
			s.trusted = strings.Trim(strings.TrimSpace(value), `"`) != ""
		case "enabled":
			s.disabled = strings.TrimSpace(value) == "false"
		}
		out[slot] = s
	}
	return out
}

// snakeCase is Codex's key label for a hook event: PreToolUse is
// pre_tool_use.
func snakeCase(s string) string {
	var b strings.Builder
	for i, r := range s {
		if unicode.IsUpper(r) {
			if i > 0 {
				b.WriteByte('_')
			}
			r = unicode.ToLower(r)
		}
		b.WriteRune(r)
	}
	return b.String()
}
