package coach

import (
	"os"
	"path/filepath"

	"go.klarlabs.de/tokenops/internal/claudehome"
	"go.klarlabs.de/tokenops/internal/hookconfig"
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
		settings, err := hookconfig.Load(f)
		if err != nil {
			continue
		}
		w.ClaudeCode.merge(hookconfig.Hooks(settings))
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

// merge records the hooks one settings file wires. Each hook is read where
// its client runs it: the end-of-turn hook on Stop, the guards on PreToolUse,
// and the subagent hook on the Agent matcher.
func (h *HarnessWiring) merge(hooks map[string]any) {
	h.Coach = h.Coach || hookconfig.Has(hooks, verbCoachHook, hookconfig.Where{Event: "Stop"})
	h.ReadGuard = h.ReadGuard || hookconfig.Has(hooks, verbReadGuard, hookconfig.Where{Event: "PreToolUse"})
	h.RouteGuard = h.RouteGuard || hookconfig.Has(hooks, verbRouteGuard, hookconfig.Where{Event: "PreToolUse", Matcher: "Agent"})
}
