package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	coachcap "go.klarlabs.de/tokenops/internal/capability/coach"
)

// presetHome is a machine with Claude Code and Codex installed and a
// tokenops config, isolated from every other test.
func presetHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
	for _, d := range []string{".claude", ".codex", ".config/tokenops"} {
		if err := os.MkdirAll(filepath.Join(home, d), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(home, ".config", "tokenops", "config.yaml"), []byte("coach:\n  autonomy: advise\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return home
}

func runPreset(t *testing.T, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	root := NewRoot()
	root.SetArgs(append([]string{"coach", "preset"}, args...))
	root.SetOut(&out)
	root.SetErr(&out)
	err := root.Execute()
	return out.String(), err
}

func TestPresetWiresEveryInstalledAgent(t *testing.T) {
	home := presetHome(t)
	out, err := runPreset(t, "autopilot", "--json")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	var r coachcap.Report
	if err := json.Unmarshal([]byte(out), &r); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	if r.Preset != "autopilot" {
		t.Errorf("preset = %q", r.Preset)
	}
	hooks := map[string]string{}
	for _, h := range r.Hooks {
		hooks[h.Client] = h.Status
	}
	if hooks["claude-code"] != "installed" || hooks["codex"] != "installed" || hooks["cursor"] != "" || hooks["opencode"] != "" {
		t.Errorf("hooks = %v; want claude-code and codex only (the agents installed)", hooks)
	}
	claude, _ := os.ReadFile(filepath.Join(home, ".claude", "settings.json"))
	for _, want := range []string{"coach-hook", "read-guard", "route-guard", `"Agent"`, "CLAUDE_CODE_AUTO_COMPACT_WINDOW"} {
		if !strings.Contains(string(claude), want) {
			t.Errorf("Claude Code settings lack %s:\n%s", want, claude)
		}
	}
	codexHooks, _ := os.ReadFile(filepath.Join(home, ".codex", "hooks.json"))
	if !strings.Contains(string(codexHooks), "coach-hook") || !strings.Contains(string(codexHooks), "route-guard") || strings.Contains(string(codexHooks), "read-guard") {
		t.Errorf("Codex hooks = %s; want coach and route guard, no read guard", codexHooks)
	}
	codexCfg, _ := os.ReadFile(filepath.Join(home, ".codex", "config.toml"))
	if !strings.Contains(string(codexCfg), "model_auto_compact_token_limit") {
		t.Errorf("Codex compaction not set:\n%s", codexCfg)
	}

	// Stepping down to observe restores the agents' compaction and keeps
	// the hooks, which record without speaking.
	if out, err := runPreset(t, "observe"); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	claude, _ = os.ReadFile(filepath.Join(home, ".claude", "settings.json"))
	if strings.Contains(string(claude), "CLAUDE_CODE_AUTO_COMPACT_WINDOW") || !strings.Contains(string(claude), "coach-hook") {
		t.Errorf("after observe:\n%s", claude)
	}
	if _, err := os.Stat(filepath.Join(home, ".codex", "config.toml")); err == nil {
		t.Error("observe left a Codex config.toml tokenops created")
	}

	// A second application changes nothing.
	out, err = runPreset(t, "observe", "--json")
	if err != nil {
		t.Fatal(err)
	}
	_ = json.Unmarshal([]byte(out), &r)
	for _, h := range r.Hooks {
		if h.Status != "current" {
			t.Errorf("re-applying observe: %s %s", h.Client, h.Status)
		}
	}
}

func TestPresetListMarksTheCurrentOne(t *testing.T) {
	presetHome(t)
	if _, err := runPreset(t, "guided"); err != nil {
		t.Fatal(err)
	}
	out, err := runPreset(t)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "* guided") || strings.Contains(out, "* advise") {
		t.Errorf("list:\n%s", out)
	}
}

func TestUnknownPresetIsRefused(t *testing.T) {
	presetHome(t)
	if _, err := runPreset(t, "yolo"); err == nil || !strings.Contains(err.Error(), "observe, advise, guided, autopilot") {
		t.Errorf("err = %v", err)
	}
}
