package routers

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func sources(t *testing.T, claude map[string]string, codex, opencode string) Sources {
	t.Helper()
	dir := t.TempDir()
	if opencode != "" {
		if err := os.WriteFile(filepath.Join(dir, "opencode.jsonc"), []byte("{\n // the router\n \"model\": \""+opencode+"\",\n}"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return Sources{
		ClaudeModels: func() map[string]string { return claude },
		CodexModel:   func() string { return codex },
		OpencodeDir:  dir,
	}
}

func TestDetectFindsEachHarnesssRouter(t *testing.T) {
	src := sources(t,
		map[string]string{"model": "opus", "env.ANTHROPIC_MODEL": "accounts/fireworks/routers/firerouter"},
		"gpt-5.6", "openrouter/auto")
	got := Detect(src)
	if len(got) != 2 {
		t.Fatalf("got %+v", got)
	}
	if got[0].Harness != ClaudeCode || got[0].Router.Name != "firerouter" || got[0].Setting != "Claude Code settings env.ANTHROPIC_MODEL" {
		t.Errorf("claude %+v", got[0])
	}
	if got[1].Harness != Opencode || got[1].Router.Name != "openrouter-auto" {
		t.Errorf("opencode %+v", got[1])
	}
	if s := got[0].StandDown(); !strings.Contains(s, "FireRouter chooses the model in Claude Code") {
		t.Errorf("stand-down %q", s)
	}
}

func TestDecides(t *testing.T) {
	none := sources(t, map[string]string{"model": "opus"}, "", "")
	fire := sources(t, map[string]string{"env.ANTHROPIC_MODEL": "firerouter"}, "", "")
	for name, tc := range map[string]struct {
		src       Sources
		requested string
		claude    bool
		want      bool
	}{
		"plain claude code":             {none, "claude-opus-5", true, false},
		"claude code behind firerouter": {fire, "claude-opus-5", true, true},
		"a router asked for directly":   {none, "openrouter/auto", false, true},
		"another harness, plain model":  {fire, "gpt-5.6", false, false},
	} {
		if got := Decides(tc.src, tc.requested, tc.claude); got != tc.want {
			t.Errorf("%s: %v, want %v", name, got, tc.want)
		}
	}
}
