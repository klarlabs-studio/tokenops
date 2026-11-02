package codexsettings

import (
	"os"
	"path/filepath"
	"testing"
)

func TestHookTrust(t *testing.T) {
	home := t.TempDir()
	hooks := filepath.Join(home, "hooks.json")
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(home, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if installed, trusted := HookTrust(home, "PreToolUse", "boundary-guard"); installed || trusted {
		t.Fatal("no hooks.json reads as installed")
	}
	write("hooks.json", `{"hooks":{
	  "Stop":[{"hooks":[{"type":"command","command":"/bin/tokenops coach-hook"}]}],
	  "PreToolUse":[
	    {"matcher":"Bash","hooks":[{"type":"command","command":"other-tool"}]},
	    {"matcher":"*","hooks":[{"type":"command","command":"x"},{"type":"command","command":"'/opt/tokenops' boundary-guard --client codex"}]}
	  ]}}`)
	if installed, trusted := HookTrust(home, "PreToolUse", "boundary-guard"); !installed || trusted {
		t.Fatalf("installed, untrusted = %v, %v", installed, trusted)
	}
	// Trust for another slot does not count.
	write("config.toml", `model = "gpt-6.1"

[hooks.state."`+hooks+`:pre_tool_use:0:0"]
trusted_hash = "sha256:aaa"

[hooks.state."`+hooks+`:pre_tool_use:1:1"]
enabled = true
`)
	if _, trusted := HookTrust(home, "PreToolUse", "boundary-guard"); trusted {
		t.Fatal("an entry without trusted_hash read as trusted")
	}
	write("config.toml", `[hooks.state."`+hooks+`:pre_tool_use:1:1"]
trusted_hash = "sha256:bbb"
[other]
`)
	if installed, trusted := HookTrust(home, "PreToolUse", "boundary-guard"); !installed || !trusted {
		t.Fatalf("trusted slot = %v, %v", installed, trusted)
	}
	// Disabled in Codex is not armed, whatever its trust.
	write("config.toml", `[hooks.state."`+hooks+`:pre_tool_use:1:1"]
trusted_hash = "sha256:bbb"
enabled = false
`)
	if _, trusted := HookTrust(home, "PreToolUse", "boundary-guard"); trusted {
		t.Fatal("a disabled hook read as armed")
	}
}
