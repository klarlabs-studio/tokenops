package coach

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"go.klarlabs.de/tokenops/internal/claudehome"
	"go.klarlabs.de/tokenops/internal/infra/opencodeplugin"
)

// Hook verbs as the hooks install writes them into each agent's config.
const (
	verbReadGuard  = "read-guard"
	verbRouteGuard = "route-guard"
	verbCoachHook  = "coach-hook"
)

// Wiring is which TokenOps hooks the agents on this machine have wired,
// the hooks autopilot acts through. Only Status fills it, from DetectWiring.
type Wiring struct {
	ClaudeCode HarnessWiring `json:"claude_code"`
	Opencode   HarnessWiring `json:"opencode"`
}

// HarnessWiring is one agent: whether it is set up here, and which hooks
// TokenOps has wired into it.
type HarnessWiring struct {
	Present bool `json:"present"`
	// ReadGuard refuses a re-read (waste).
	ReadGuard bool `json:"read_guard"`
	// RouteGuard is Claude Code's subagent hook, which moves a subagent to a
	// cheaper model (models).
	RouteGuard bool `json:"route_guard"`
	// Coach is the end-of-turn hook, which sets Claude Code's default model
	// (models).
	Coach bool `json:"coach"`
}

// WiringSources says where each agent keeps its configuration. Zero values
// read the real locations.
type WiringSources struct {
	ClaudeDir         string
	ClaudeSettings    []string
	OpencodeDir       string
	OpencodePluginDir string
}

// DetectWiring reads which TokenOps hooks Claude Code and opencode have
// wired, and which of them are set up on this machine.
func DetectWiring(src WiringSources) Wiring {
	src = src.withDefaults()
	var w Wiring
	w.ClaudeCode.Present = isDir(src.ClaudeDir)
	for _, f := range src.ClaudeSettings {
		b, err := os.ReadFile(f) //nolint:gosec // the operator's own client settings
		if err != nil {
			continue
		}
		var settings struct {
			Hooks map[string]json.RawMessage `json:"hooks"`
		}
		if json.Unmarshal(b, &settings) != nil {
			continue
		}
		w.ClaudeCode.merge(settings.Hooks)
	}
	w.Opencode.Present = isDir(src.OpencodeDir)
	if b, err := os.ReadFile(filepath.Join(src.OpencodePluginDir, opencodeplugin.FileName)); err == nil { //nolint:gosec // TokenOps' own plugin
		text := string(b)
		w.Opencode.ReadGuard = opencodeplugin.Has(text, verbReadGuard)
		w.Opencode.Coach = opencodeplugin.Has(text, verbCoachHook)
	}
	return w
}

func (src WiringSources) withDefaults() WiringSources {
	home, _ := os.UserHomeDir()
	if src.ClaudeDir == "" {
		src.ClaudeDir = claudehome.Dir()
	}
	if src.ClaudeSettings == nil {
		src.ClaudeSettings = claudehome.SettingsFiles()
	}
	if src.OpencodeDir == "" {
		src.OpencodeDir = filepath.Join(home, ".config", "opencode")
	}
	if src.OpencodePluginDir == "" {
		src.OpencodePluginDir = filepath.Join(src.OpencodeDir, "plugins")
	}
	return src
}

func isDir(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.IsDir()
}

// merge records the hooks one settings file wires. Claude Code keeps a
// hook under its event, in groups that each name a matcher and hold the
// commands; a command is ours when its first argument is our verb.
func (h *HarnessWiring) merge(hooks map[string]json.RawMessage) {
	var stop, preTool []claudeGroup
	_ = json.Unmarshal(hooks["Stop"], &stop)
	_ = json.Unmarshal(hooks["PreToolUse"], &preTool)
	h.Coach = h.Coach || anyCommand(stop, "", verbCoachHook)
	h.ReadGuard = h.ReadGuard || anyCommand(preTool, "", verbReadGuard)
	h.RouteGuard = h.RouteGuard || anyCommand(preTool, "Agent", verbRouteGuard)
}

type claudeGroup struct {
	Matcher string        `json:"matcher"`
	Hooks   []claudeEntry `json:"hooks"`
}

type claudeEntry struct {
	Command string   `json:"command"`
	Args    []string `json:"args"`
}

// anyCommand reports whether a group with the matcher (any when empty)
// runs verb.
func anyCommand(groups []claudeGroup, matcher, verb string) bool {
	for _, g := range groups {
		if matcher != "" && g.Matcher != matcher {
			continue
		}
		for _, e := range g.Hooks {
			if e.verb() == verb {
				return true
			}
		}
	}
	return false
}

// verb is the TokenOps verb an entry runs: args[0] when the entry has an
// args array, else the word after the binary on its command line.
func (e claudeEntry) verb() string {
	if len(e.Args) > 0 {
		return e.Args[0]
	}
	if f := strings.Fields(e.Command); len(f) > 1 {
		return f[1]
	}
	return ""
}
