package cli

import (
	"os"
	"path/filepath"
	"testing"

	"go.klarlabs.de/tokenops/internal/capability/boundaries"
	coachcap "go.klarlabs.de/tokenops/internal/capability/coach"
	"go.klarlabs.de/tokenops/internal/claudehome"
)

// Three readers decide whether a TokenOps hook is wired in a Claude Code
// settings file: `hooks status`, the coach's wiring (which autopilot acts
// through) and the boundary status. For the same file they must agree.
//
// Every path is isolated: HOME, XDG_CONFIG_HOME, the Claude config dir and
// the managed settings dir all point into the test's temporary directory.
func TestHookDetectionAgreesAcrossSurfaces(t *testing.T) {
	const subagent = "claude-code/subagent"
	cases := []struct {
		name string
		// install flags run through `hooks install`, or nothing when empty.
		install []string
		// settings is written verbatim when install is empty.
		settings string
		// want is the answer every surface must give.
		want struct{ readGuard, routeGuard, coach, boundary bool }
	}{
		{
			name:     "nothing wired",
			settings: `{"model":"opus"}`,
		},
		{
			name:    "every guard installed",
			install: []string{"--coach", "--read-guard", "--route-guard", "--boundary"},
			want:    struct{ readGuard, routeGuard, coach, boundary bool }{true, true, true, true},
		},
		{
			name:    "read guard only",
			install: []string{"--read-guard"},
			want:    struct{ readGuard, routeGuard, coach, boundary bool }{readGuard: true},
		},
		{
			name:     "boundary hook behind a quoted binary path with spaces",
			settings: `{"hooks":{"PreToolUse":[{"matcher":"Bash","hooks":[{"type":"command","command":"'/Applications/My Tools/tokenops' boundary-guard"}]}]}}`,
			want:     struct{ readGuard, routeGuard, coach, boundary bool }{boundary: true},
		},
		{
			name:     "the verb only appears as a string value, which is not a hook",
			settings: `{"env":{"NOTE":"boundary-guard route-guard Agent"},"hooks":{"PreToolUse":[{"matcher":"Bash","hooks":[{"command":"/usr/local/bin/other"}]}]}}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			claudeDir := filepath.Join(home, ".claude")
			pluginDir := filepath.Join(home, ".config", "opencode", "plugins")
			t.Setenv("HOME", home)
			t.Setenv("XDG_CONFIG_HOME", home)
			t.Setenv(claudehome.EnvVar, claudeDir)
			prevManaged := claudehome.ManagedDir
			claudehome.ManagedDir = t.TempDir()
			t.Cleanup(func() { claudehome.ManagedDir = prevManaged })

			path := filepath.Join(claudeDir, "settings.json")
			if len(tc.install) > 0 {
				args := append([]string{"install"}, tc.install...)
				args = append(args, "--settings", path)
				if _, err := runHooksCapturing(t, args...); err != nil {
					t.Fatalf("install %v: %v", tc.install, err)
				}
			} else {
				if err := os.MkdirAll(claudeDir, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(tc.settings), 0o600); err != nil {
					t.Fatal(err)
				}
			}

			st, err := hooksStatusOf(hookClientClaudeCode, path, "/usr/local/bin/tokenops")
			if err != nil {
				t.Fatalf("hooks status: %v", err)
			}
			statusSees := map[string]bool{}
			for _, h := range st.Hooks {
				statusSees[h.Hook] = true
			}

			w := coachcap.DetectWiring(coachcap.WiringSources{
				ClaudeDir:         claudeDir,
				ClaudeSettings:    []string{path},
				OpencodeDir:       filepath.Join(home, ".config", "opencode"),
				OpencodePluginDir: pluginDir,
			}).ClaudeCode
			bound := boundaries.Installed(path, pluginDir)

			got := []struct {
				surface, hook string
				seen, want    bool
			}{
				{"hooks status", "read-guard", statusSees["read-guard"], tc.want.readGuard},
				{"coach wiring", "read-guard", w.ReadGuard, tc.want.readGuard},
				{"hooks status", "route-guard", statusSees["route-guard"], tc.want.routeGuard},
				{"coach wiring", "route-guard", w.RouteGuard, tc.want.routeGuard},
				{"boundary status", subagent, bound[subagent], tc.want.routeGuard},
				{"hooks status", "coach-hook", statusSees["coach-hook"], tc.want.coach},
				{"coach wiring", "coach-hook", w.Coach, tc.want.coach},
				{"hooks status", "boundary-guard", statusSees["boundary-guard"], tc.want.boundary},
				{"boundary status", "boundary-guard", bound[boundaries.HarnessClaudeCode], tc.want.boundary},
			}
			for _, g := range got {
				if g.seen != g.want {
					t.Errorf("%s says %s wired = %v, want %v", g.surface, g.hook, g.seen, g.want)
				}
			}
		})
	}
}
