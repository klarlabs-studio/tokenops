package coach

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.klarlabs.de/tokenops/internal/config"
)

const fixFor = "tokenops hooks install --read-guard --coach --route-guard --client "

func autonomous(names ...string) []Power {
	out := make([]Power, 0, len(names))
	for _, n := range names {
		out = append(out, Power{Name: n, Effective: config.AutonomyAutonomous})
	}
	return out
}

func TestReachOf(t *testing.T) {
	applied := []LeverResult{{Client: "claude-code", Status: "applied"}}
	unavailable := []LeverResult{{Client: "claude-code", Status: "unavailable"}}
	tests := []struct {
		name      string
		powers    []Power
		wiring    Wiring
		levers    []LeverResult
		wantNil   bool
		wantActs  bool
		wantFix   string
		wantWhy   []string
		wantNoFix bool
	}{
		{
			name:    "no power autonomous is no reach to report",
			powers:  []Power{{Name: config.PowerWaste, Effective: config.AutonomyAdvise}},
			wantNil: true,
		},
		{
			name:    "context alone is a setting, not a hook-gated act, so nothing to report",
			powers:  autonomous(config.PowerContext),
			levers:  unavailable,
			wantNil: true,
		},
		{
			name:     "read guard on Claude Code lets waste act",
			powers:   autonomous(config.PowerWaste),
			wiring:   Wiring{ClaudeCode: HarnessWiring{Present: true, ReadGuard: true}},
			wantActs: true,
		},
		{
			name:     "read guard on opencode lets waste act",
			powers:   autonomous(config.PowerWaste),
			wiring:   Wiring{Opencode: HarnessWiring{Present: true, ReadGuard: true}},
			wantActs: true,
		},
		{
			name:     "coach hook alone lets models set the default model",
			powers:   autonomous(config.PowerModels),
			wiring:   Wiring{ClaudeCode: HarnessWiring{Present: true, Coach: true}},
			wantActs: true,
		},
		{
			name:     "an applied compaction setting does not count as an act",
			powers:   autonomous(config.PowerWaste),
			wiring:   Wiring{ClaudeCode: HarnessWiring{Present: true}},
			levers:   applied,
			wantFix:  fixFor + "claude-code",
			wantWhy:  []string{"read guard in Claude Code or opencode"},
			wantActs: false,
		},
		{
			name:    "waste with no read guard names the Claude Code fix",
			powers:  autonomous(config.PowerWaste),
			wiring:  Wiring{ClaudeCode: HarnessWiring{Present: true}},
			wantFix: fixFor + "claude-code",
			wantWhy: []string{"read guard in Claude Code or opencode"},
		},
		{
			name:    "opencode only names the opencode fix",
			powers:  autonomous(config.PowerWaste),
			wiring:  Wiring{Opencode: HarnessWiring{Present: true}},
			wantFix: fixFor + "opencode",
			wantWhy: []string{"read guard in Claude Code or opencode"},
		},
		{
			name:    "models with no hooks says what it needs",
			powers:  autonomous(config.PowerModels),
			wiring:  Wiring{ClaudeCode: HarnessWiring{Present: true}},
			wantFix: fixFor + "claude-code",
			wantWhy: []string{"route guard and the coach hook in Claude Code"},
		},
		{
			name:    "both powers without hooks name both needs once",
			powers:  autonomous(config.PowerWaste, config.PowerModels),
			wiring:  Wiring{ClaudeCode: HarnessWiring{Present: true}},
			wantFix: fixFor + "claude-code",
			wantWhy: []string{"read guard in Claude Code or opencode", "route guard and the coach hook in Claude Code"},
		},
		{
			name:      "no agent installed here has no fix to offer",
			powers:    autonomous(config.PowerWaste),
			wantNoFix: true,
			wantWhy:   []string{"No Claude Code or opencode config was found here"},
		},
		{
			name:     "one acting power is enough even if another is stuck",
			powers:   autonomous(config.PowerWaste, config.PowerModels),
			wiring:   Wiring{ClaudeCode: HarnessWiring{Present: true, RouteGuard: true}},
			wantActs: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := reachOf(tt.powers, tt.wiring, tt.levers)
			if tt.wantNil {
				if got != nil {
					t.Fatalf("reachOf = %+v, want nil", got)
				}
				return
			}
			if got == nil {
				t.Fatal("reachOf = nil, want a reach")
			}
			if got.Acts != tt.wantActs {
				t.Fatalf("Acts = %v, want %v (%+v)", got.Acts, tt.wantActs, got)
			}
			if tt.wantActs {
				if got.Why != "" || got.Fix != "" {
					t.Errorf("an acting autopilot says no why or fix, got %+v", got)
				}
				return
			}
			if got.Fix != tt.wantFix {
				t.Errorf("Fix = %q, want %q", got.Fix, tt.wantFix)
			}
			if tt.wantNoFix && got.Fix != "" {
				t.Errorf("Fix = %q, want none", got.Fix)
			}
			for _, w := range tt.wantWhy {
				if !strings.Contains(got.Why, w) {
					t.Errorf("Why = %q, want it to contain %q", got.Why, w)
				}
			}
		})
	}
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestDetectWiring(t *testing.T) {
	const readGuardPre = `{"hooks":{"PreToolUse":[{"matcher":"Read","hooks":[{"type":"command","command":"/x/tokenops","args":["read-guard"]}]}]}}`
	const coachStop = `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"/x/tokenops","args":["coach-hook"]}]}]}}`
	const routeAgent = `{"hooks":{"PreToolUse":[{"matcher":"Agent","hooks":[{"type":"command","command":"/x/tokenops","args":["route-guard"]}]}]}}`
	const routePrompt = `{"hooks":{"UserPromptSubmit":[{"hooks":[{"type":"command","command":"/x/tokenops","args":["route-guard"]}]}]}}`
	const commandOnly = `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"/x/tokenops coach-hook"}]}]}}`
	const opencodeSrc = "await run([\"read-guard\"], {});\nawait run([\"coach-hook\"], {});\n"

	tests := []struct {
		name     string
		settings string
		plugin   string
		want     HarnessWiring
		wantOC   HarnessWiring
	}{
		{name: "nothing wired", settings: `{}`, want: HarnessWiring{Present: true}},
		{name: "read guard", settings: readGuardPre, want: HarnessWiring{Present: true, ReadGuard: true}},
		{name: "coach hook", settings: coachStop, want: HarnessWiring{Present: true, Coach: true}},
		{name: "subagent route guard", settings: routeAgent, want: HarnessWiring{Present: true, RouteGuard: true}},
		{name: "per-turn route guard is not the subagent hook", settings: routePrompt, want: HarnessWiring{Present: true}},
		{name: "command-only shape", settings: commandOnly, want: HarnessWiring{Present: true, Coach: true}},
		{name: "malformed settings wire nothing", settings: `{"hooks":`, want: HarnessWiring{Present: true}},
		{
			name: "opencode plugin", settings: `{}`, plugin: opencodeSrc,
			want:   HarnessWiring{Present: true},
			wantOC: HarnessWiring{Present: true, ReadGuard: true, Coach: true},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			claudeDir := filepath.Join(root, "claude")
			ocDir := filepath.Join(root, "opencode")
			settings := filepath.Join(claudeDir, "settings.json")
			writeFile(t, settings, tt.settings)
			if tt.plugin != "" {
				writeFile(t, filepath.Join(ocDir, "plugins", "tokenops-read-guard.ts"), tt.plugin)
			} else {
				if err := os.MkdirAll(ocDir, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			got := DetectWiring(WiringSources{
				ClaudeDir: claudeDir, ClaudeSettings: []string{settings},
				OpencodeDir: ocDir, OpencodePluginDir: filepath.Join(ocDir, "plugins"),
			})
			if got.ClaudeCode != tt.want {
				t.Errorf("Claude Code = %+v, want %+v", got.ClaudeCode, tt.want)
			}
			wantOC := tt.wantOC
			if wantOC == (HarnessWiring{}) {
				wantOC = HarnessWiring{Present: true}
			}
			if got.Opencode != wantOC {
				t.Errorf("opencode = %+v, want %+v", got.Opencode, wantOC)
			}
		})
	}
}

func TestDetectWiringAbsentAgents(t *testing.T) {
	root := t.TempDir()
	got := DetectWiring(WiringSources{
		ClaudeDir: filepath.Join(root, "no-claude"), ClaudeSettings: []string{filepath.Join(root, "no-claude", "settings.json")},
		OpencodeDir: filepath.Join(root, "no-opencode"), OpencodePluginDir: filepath.Join(root, "no-opencode", "plugins"),
	})
	if got != (Wiring{}) {
		t.Fatalf("DetectWiring with no agents = %+v, want zero", got)
	}
}
