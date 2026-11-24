package connections

import (
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/contexts/observability/freshness"
	"go.klarlabs.de/tokenops/internal/contexts/spend/plans"
)

var now = time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)

// What the configuration says about each plan source: connected by the
// operator, on by default, or merely running.
func TestSourcesFollowTheConfig(t *testing.T) {
	on := true
	tests := []struct {
		name string
		cfg  func(*config.Config)
		tag  string
		want bool
	}{
		{"cursor off", func(*config.Config) {}, "cursor-web", false},
		{"cursor enabled", func(c *config.Config) { c.VendorUsage.Cursor.Enabled = true }, "cursor-web", true},
		{"codex app server, on by default", func(*config.Config) {}, "codex-app-server", true},
		{"codex app server switched off", func(c *config.Config) { off := false; c.VendorUsage.CodexAppServer.Enabled = &off }, "codex-app-server", false},
		{"the status line runs always, given nothing", func(*config.Config) {}, "claude-code-statusline", false},
		{"an account reader given nothing", func(*config.Config) {}, "openrouter-account", false},
		{"an account reader with a stored key", func(c *config.Config) {
			c.VendorUsage.Accounts.Credentials = map[string]config.AccountCredential{"openrouter": {Key: "k"}}
		}, "openrouter-account", true},
		{"stored key, account readers switched off", func(c *config.Config) {
			off := false
			c.VendorUsage.Accounts.Enabled = &off
			c.VendorUsage.Accounts.Credentials = map[string]config.AccountCredential{"openrouter": {Key: "k"}}
		}, "openrouter-account", false},
		{"an app login granted", func(c *config.Config) {
			c.VendorUsage.Accounts.Enabled = &on
			c.VendorUsage.Grants = map[string]config.AppLoginGrant{"antigravity": {App: "Antigravity"}}
		}, "antigravity-local", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var cfg config.Config
			tt.cfg(&cfg)
			for _, s := range sourcesOf(cfg, nil) {
				if s.Tag == tt.tag {
					if s.Switched != tt.want {
						t.Errorf("switched = %v, want %v", s.Switched, tt.want)
					}
					return
				}
			}
			t.Fatalf("no plan source %s", tt.tag)
		})
	}
}

// From the glance's turns, the readers' health and the configuration to
// what a surface shows.
func TestCompute(t *testing.T) {
	turns := map[string]plans.ProviderTurns{
		"cursor":    {Turns: 41, LastAt: now.Add(-2 * time.Hour)},
		"anthropic": {Turns: 300, LastAt: now.Add(-time.Minute)},
		"github":    {Turns: 3, LastAt: now.Add(-time.Hour)},
	}
	var cfg config.Config
	cfg.VendorUsage.GitHubCopilot.Enabled = true
	cfg.DismissProvider("factory", true)
	health := []freshness.Report{
		{Tag: "claude-code-statusline", State: freshness.StateHealthy, LastEventAt: now.Add(-time.Minute)},
		{Tag: "github-copilot", State: freshness.StateFailing, LastError: "401 Unauthorized"},
	}
	got := Compute(Inputs{Config: &cfg, Turns: turns, Health: health, Now: now,
		Installed: map[string][]string{"factory": {"droid on PATH"}}})
	states := map[string]State{}
	for _, c := range got {
		states[c.Provider] = c.State
	}
	want := map[string]State{"cursor": NotConnected, "anthropic": Reading, "github": Failing, "factory": Dismissed}
	for p, s := range want {
		if states[p] != s {
			t.Errorf("%s = %q, want %q (all: %v)", p, states[p], s, states)
		}
	}
	if len(states) != len(want) {
		t.Errorf("states = %v, want %v", states, want)
	}
	if d := Detected(got); len(d) != 1 || d[0].Provider != "cursor" {
		t.Errorf("Detected = %+v, want cursor alone", d)
	}
}
