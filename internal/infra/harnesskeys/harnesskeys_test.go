package harnesskeys

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"go.klarlabs.de/tokenops/internal/infra/codexsettings"
)

func TestFindEveryHarness(t *testing.T) {
	data, conf := t.TempDir(), t.TempDir()
	write := func(path, body string) {
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(data, "auth.json"), `{
		"openrouter": {"type": "api", "key": "sk-or-auth"},
		"anthropic": {"type": "oauth", "access": "subscription-token", "refresh": "r"}
	}`)
	write(filepath.Join(conf, "deepseek.key"), "sk-ds-file\n")
	write(filepath.Join(conf, "opencode.jsonc"), `{
		// a comment, with a URL in a string below
		"$schema": "https://opencode.ai/config.json",
		"provider": {
			"deepseek": {"options": {"apiKey": "{file:deepseek.key}"}},
			"moonshotai": {"options": {"apiKey": "{env:MOONSHOT_KEY}", "baseURL": "https://api.moonshot.ai/v1"}}, /* trailing */
		},
	}`)
	env := map[string]string{"MOONSHOT_KEY": "sk-ms-env", "CODEX_OR": "sk-or-codex", "DEEPSEEK_API_KEY": "sk-ds-env"}
	got := Find(Options{
		Getenv:         func(k string) string { return env[k] },
		OpencodeData:   data,
		OpencodeConfig: conf,
		Claude:         func() (string, string) { return "https://openrouter.ai/api", "sk-or-claude" },
		Codex: func() map[string]codexsettings.Provider {
			return map[string]codexsettings.Provider{
				"openrouter": {BaseURL: "https://openrouter.ai/api/v1", EnvKey: "CODEX_OR"},
				"fireworks":  {BaseURL: "https://api.fireworks.ai/inference/v1", BearerToken: "fw_codex"},
				"nokey":      {BaseURL: "https://x.example"},
			}
		},
	})
	keys := make([]string, 0, len(got))
	for _, c := range got {
		keys = append(keys, c.Origin+"|"+c.BaseURL+"|"+c.ProviderID+"|"+c.Key)
	}
	sort.Strings(keys)
	want := []string{
		"$DEEPSEEK_API_KEY||deepseek|sk-ds-env",
		"Claude Code settings|https://openrouter.ai/api||sk-or-claude",
		"Codex config|https://api.fireworks.ai/inference/v1||fw_codex",
		"Codex config|https://openrouter.ai/api/v1||sk-or-codex",
		"opencode auth.json||openrouter|sk-or-auth",
		"opencode config|https://api.moonshot.ai/v1|moonshotai|sk-ms-env",
		"opencode config||deepseek|sk-ds-file",
	}
	if len(keys) != len(want) {
		t.Fatalf("got\n%v\nwant\n%v", keys, want)
	}
	for i := range want {
		if keys[i] != want[i] {
			t.Errorf("got %q, want %q", keys[i], want[i])
		}
	}
}

func TestStripJSONCKeepsStrings(t *testing.T) {
	src := `{"url": "https://a//b", "s": "/* not a comment */", // c
	"list": [1, 2,], /* block */ "esc": "a\"//b",}`
	var v map[string]any
	if err := json.Unmarshal(StripJSONC([]byte(src)), &v); err != nil {
		t.Fatalf("%v: %s", err, StripJSONC([]byte(src)))
	}
	if v["url"] != "https://a//b" || v["s"] != "/* not a comment */" || v["esc"] != `a"//b` {
		t.Errorf("%v", v)
	}
}
