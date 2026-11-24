package providers

import (
	"strings"
	"testing"
)

// The one action to connect a provider's plan, for the kinds of source the
// registry has: a bespoke source's own, an account reader's derived from
// its credential, a plan before an organisation's spend, nothing for a
// plan read by itself.
func TestConnectAction(t *testing.T) {
	tests := []struct {
		provider, source, command, needs string
		none                             bool
	}{
		{provider: "cursor", source: "cursor-web", command: "TOKENOPS_CURSOR_COOKIE=… tokenops vendor-usage enable cursor", needs: "WorkosCursorSessionToken"},
		{provider: "anthropic", source: "claude-code-statusline", command: "tokenops statusline install", needs: "nothing secret"},
		{provider: "github", source: "github-copilot", command: "tokenops vendor-usage enable github-copilot", needs: "nothing to paste"},
		{provider: "fireworks", source: "fireworks-usage", command: "tokenops vendor-usage enable fireworks", needs: "Fireworks key"},
		{provider: "openrouter", source: "openrouter-account", command: "tokenops vendor-usage setup openrouter", needs: "an API key from OpenRouter"},
		{provider: "perplexity", source: "perplexity-account", command: "tokenops vendor-usage setup perplexity", needs: "browser session"},
		{provider: "stepfun", source: "stepfun-account", command: "tokenops vendor-usage setup stepfun", needs: "username and password"},
		{provider: "antigravity", source: "antigravity-local", command: "tokenops vendor-usage setup antigravity --use-app-login", needs: "your yes"},
		{provider: "zed", source: "zed-account", command: "tokenops vendor-usage setup zed", needs: "Keychain"},
		{provider: "bedrock", source: "bedrock-cost-explorer", command: "tokenops vendor-usage setup bedrock", needs: "credentials already on this machine"},
		{provider: "litellm", source: "litellm-account", command: "tokenops vendor-usage setup litellm", needs: "gateway's address"},
		// Amp's CLI reads by itself; the key is what the operator can add.
		{provider: "amp", source: "amp-account", command: "tokenops vendor-usage setup amp", needs: "an API key from Amp"},
		// Codex's plan is read by the app server and the rollouts by
		// themselves; the API organisation's admin key is no plan.
		{provider: "openai", none: true},
		{provider: "coderabbit", none: true},
		{provider: "gemini", none: true},
		{provider: "cerebras", none: true},
	}
	for _, tt := range tests {
		t.Run(tt.provider, func(t *testing.T) {
			d, ok := Lookup(tt.provider)
			if !ok {
				t.Fatalf("no provider %q", tt.provider)
			}
			a, ok := d.ConnectAction()
			if tt.none {
				if ok {
					t.Fatalf("ConnectAction = %+v, want none", a)
				}
				return
			}
			if !ok {
				t.Fatal("no action")
			}
			if a.Source != tt.source || a.Command != tt.command || !strings.Contains(a.Needs, tt.needs) {
				t.Errorf("ConnectAction = %+v, want source %s, command %q, needs ~%q", a, tt.source, tt.command, tt.needs)
			}
		})
	}
}

// A presence probe names one path, of a kind the probe can check without
// a prompt: never under ~/Library, where macOS guards app data.
func TestPresenceProbesAreCheap(t *testing.T) {
	for _, d := range All() {
		for _, p := range d.Presence {
			set := 0
			for _, v := range []string{p.App, p.Dir, p.Binary} {
				if v != "" {
					set++
				}
			}
			switch {
			case set != 1:
				t.Errorf("%s: presence %+v sets %d fields, want exactly one", d.ID, p, set)
			case p.App != "" && (!strings.HasSuffix(p.App, ".app") || strings.Contains(p.App, "/")):
				t.Errorf("%s: app %q is a bundle name like Cursor.app", d.ID, p.App)
			case p.Binary != "" && strings.Contains(p.Binary, "/"):
				t.Errorf("%s: binary %q is a name on PATH", d.ID, p.Binary)
			case p.Dir != "" && !strings.HasPrefix(p.Dir, "~/") && !strings.HasPrefix(p.Dir, "$XDG_CONFIG_HOME/") && !strings.HasPrefix(p.Dir, "$XDG_DATA_HOME/"):
				t.Errorf("%s: dir %q starts with ~/, $XDG_CONFIG_HOME/ or $XDG_DATA_HOME/", d.ID, p.Dir)
			case strings.HasPrefix(p.Dir, "~/Library"):
				t.Errorf("%s: dir %q is under ~/Library, which macOS may guard behind a prompt", d.ID, p.Dir)
			}
		}
		if len(d.Presence) > 0 && d.CatalogOnly != "" {
			t.Errorf("%s: a catalog-only provider has nothing to connect; drop its presence probes", d.ID)
		}
	}
}
