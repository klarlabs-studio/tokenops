package compactlever

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

var now = time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)

func paths(t *testing.T) Paths {
	t.Helper()
	d := t.TempDir()
	for _, sub := range []string{".claude", ".codex", ".config/opencode", ".cache/opencode"} {
		if err := os.MkdirAll(filepath.Join(d, sub), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	return Paths{
		ClaudeSettings: filepath.Join(d, ".claude", "settings.json"),
		CodexConfig:    filepath.Join(d, ".codex", "config.toml"),
		OpencodeConfig: filepath.Join(d, ".config", "opencode", "opencode.json"),
		OpencodeModels: filepath.Join(d, ".cache", "opencode", "models.json"),
		State:          filepath.Join(d, ".tokenops", "coach", "compaction.json"),
	}
}

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func jsonOf(t *testing.T, path string) map[string]any {
	t.Helper()
	m := map[string]any{}
	if err := json.Unmarshal([]byte(read(t, path)), &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func byClient(rs []Result, client string) []Result {
	var out []Result
	for _, r := range rs {
		if r.Client == client {
			out = append(out, r)
		}
	}
	return out
}

const codexToml = `model = "gpt-5.5"
approval_policy = "on-request"

[mcp_servers.tokenops]
command = "tokenops"
`

const opencodeCatalog = `{
 "anthropic": {"models": {
   "claude-opus-4-8": {"limit": {"context": 1000000, "output": 64000}},
   "claude-haiku-4-5": {"limit": {"context": 200000, "output": 64000}}
 }},
 "opencode": {"models": {"big-pickle": {"limit": {"context": 200000, "input": 160000, "output": 32000}}}}
}`

const opencodeConfig = `{"$schema":"https://opencode.ai/config.json","plugin":["tokenops"],"provider":{"anthropic":{"options":{"timeout":600000}}}}`

func plan() Plan {
	return Plan{
		ClaudeCompactAt: 600_000, CodexCompactAt: 150_000,
		OpencodeModels: []string{"anthropic/claude-opus-4-8", "opencode/big-pickle", "anthropic/claude-haiku-4-5", "nope/missing"},
		OpencodeShare:  0.6,
	}
}

func TestApplyAndRevertRestoreEveryFileExactly(t *testing.T) {
	p := paths(t)
	// Unsorted keys and odd indentation: a revert must give them back as
	// they were, not re-serialised.
	const claudeOriginal = "{\n    \"model\": \"opus\",\n    \"hooks\": {\"Stop\": []}\n}\n"
	write(t, p.ClaudeSettings, claudeOriginal)
	write(t, p.CodexConfig, codexToml)
	write(t, p.OpencodeConfig, opencodeConfig)
	write(t, p.OpencodeModels, opencodeCatalog)

	rs, err := Apply(p, plan(), now)
	if err != nil {
		t.Fatal(err)
	}
	if c := byClient(rs, ClientClaudeCode); len(c) != 1 || c[0].Status != StatusApplied || c[0].Value != 633_000 {
		t.Fatalf("claude = %+v", c)
	}
	if got := jsonOf(t, p.ClaudeSettings); got["env"].(map[string]any)["CLAUDE_CODE_AUTO_COMPACT_WINDOW"] != "633000" || got["model"] != "opus" || got["hooks"] == nil {
		t.Errorf("claude settings = %v", got)
	}
	toml := read(t, p.CodexConfig)
	if v, ok := tomlTopLevelInt(toml, codexKey); !ok || v != 150_000 {
		t.Errorf("codex key not set at top level:\n%s", toml)
	}
	if strings.Index(toml, codexKey) > strings.Index(toml, "[mcp_servers") {
		t.Errorf("codex key written inside a table:\n%s", toml)
	}
	oc := byClient(rs, ClientOpencode)
	statuses := map[string]Status{}
	for _, r := range oc {
		statuses[r.Key] = r.Status
	}
	want := map[string]Status{
		"anthropic/claude-opus-4-8": StatusApplied, "opencode/big-pickle": StatusApplied,
		"anthropic/claude-haiku-4-5": StatusApplied, "nope/missing": StatusUnavailable,
	}
	if !reflect.DeepEqual(statuses, want) {
		t.Errorf("opencode statuses = %v; want %v", statuses, want)
	}
	lim, _ := opencodeLimit(jsonOf(t, p.OpencodeConfig), "anthropic/claude-opus-4-8")
	if lim["input"] != float64(620_000) || lim["context"] != float64(1_000_000) || lim["output"] != float64(64_000) {
		t.Errorf("opus limit = %v; want context and output kept, input 600k + 20k reserved", lim)
	}
	if c := byClient(rs, ClientCursor); len(c) != 1 || c[0].Status != StatusUnavailable {
		t.Errorf("cursor = %+v", c)
	}

	again, err := Apply(p, plan(), now)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range again {
		if r.Status == StatusApplied {
			t.Errorf("second apply changed %s %s", r.Client, r.Key)
		}
	}
	for _, r := range Check(p) {
		if r.Status != StatusActive {
			t.Errorf("check %s %s = %s", r.Client, r.Key, r.Status)
		}
	}

	if _, err := Revert(p); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]string{p.ClaudeSettings: claudeOriginal, p.CodexConfig: codexToml, p.OpencodeConfig: opencodeConfig} {
		if got := read(t, path); got != want {
			t.Errorf("%s after revert:\n%q\nwant byte for byte\n%q", filepath.Base(path), got, want)
		}
	}
	if len(Check(p)) != 0 {
		t.Error("state not cleared after revert")
	}
}

// A value the operator set is theirs: never overwritten, never removed.
func TestOperatorValuesAreLeftAlone(t *testing.T) {
	p := paths(t)
	write(t, p.ClaudeSettings, `{"autoCompactWindow":800000}`)
	write(t, p.CodexConfig, "model_auto_compact_token_limit = 200000\n"+codexToml)
	write(t, p.OpencodeConfig, `{"provider":{"anthropic":{"models":{"claude-opus-4-8":{"limit":{"context":1000000,"input":700000,"output":64000}}}}}}`)
	write(t, p.OpencodeModels, opencodeCatalog)
	before := map[string]string{"c": read(t, p.ClaudeSettings), "x": read(t, p.CodexConfig), "o": read(t, p.OpencodeConfig)}
	rs, err := Apply(p, Plan{ClaudeCompactAt: 600_000, CodexCompactAt: 150_000, OpencodeModels: []string{"anthropic/claude-opus-4-8"}, OpencodeShare: 0.6}, now)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rs {
		if r.Client != ClientCursor && r.Status != StatusYours {
			t.Errorf("%s %s = %s; want yours", r.Client, r.Key, r.Status)
		}
	}
	if _, err := Revert(p); err != nil {
		t.Fatal(err)
	}
	after := map[string]string{"c": read(t, p.ClaudeSettings), "x": read(t, p.CodexConfig), "o": read(t, p.OpencodeConfig)}
	if !reflect.DeepEqual(before, after) {
		t.Errorf("operator files changed:\n%v\n%v", before, after)
	}
}

// A value tokenops wrote that the operator later changed is theirs now.
func TestEditedValuesSurviveRevert(t *testing.T) {
	p := paths(t)
	write(t, p.ClaudeSettings, `{}`)
	if _, err := Apply(p, Plan{ClaudeCompactAt: 600_000}, now); err != nil {
		t.Fatal(err)
	}
	write(t, p.ClaudeSettings, `{"env":{"CLAUDE_CODE_AUTO_COMPACT_WINDOW":"500000"}}`)
	if c := Check(p); len(c) != 1 || c[0].Status != StatusEdited {
		t.Fatalf("check = %+v; want edited", c)
	}
	rs, err := Revert(p)
	if err != nil || len(rs) != 1 || rs[0].Status != StatusEdited {
		t.Fatalf("revert = %+v, %v", rs, err)
	}
	if got := jsonOf(t, p.ClaudeSettings)["env"].(map[string]any)["CLAUDE_CODE_AUTO_COMPACT_WINDOW"]; got != "500000" {
		t.Errorf("operator's edit lost: %v", got)
	}
}

func TestMissingClientsAreReportedNotCreated(t *testing.T) {
	d := t.TempDir()
	p := Paths{
		ClaudeSettings: filepath.Join(d, "none", ".claude", "settings.json"),
		CodexConfig:    filepath.Join(d, "none", ".codex", "config.toml"),
		OpencodeConfig: filepath.Join(d, "none", "opencode.json"),
		OpencodeModels: filepath.Join(d, "none", "models.json"),
		State:          filepath.Join(d, "state.json"),
	}
	rs, err := Apply(p, plan(), now)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rs {
		if r.Status != StatusUnavailable {
			t.Errorf("%s = %s; want unavailable", r.Client, r.Status)
		}
	}
	for _, f := range []string{p.ClaudeSettings, p.CodexConfig, p.OpencodeConfig} {
		if _, err := os.Stat(f); err == nil {
			t.Errorf("created %s for a client that is not set up", f)
		}
	}
}

func TestTomlTopLevelEdits(t *testing.T) {
	for name, src := range map[string]string{
		"empty":       "",
		"no tables":   "model = \"x\"\n",
		"tables":      codexToml,
		"only tables": "[a]\nb = 1\n",
		"no gap":      "model = \"x\"\n[a]\nb = 1\n",
	} {
		out := tomlSetTopLevel(src, codexKey, 150_000)
		if v, ok := tomlTopLevelInt(out, codexKey); !ok || v != 150_000 {
			t.Errorf("%s: not set:\n%s", name, out)
		}
		if back := tomlRemoveTopLevel(out, codexKey); back != src && strings.TrimRight(back, "\n") != strings.TrimRight(src, "\n") {
			t.Errorf("%s: remove did not restore:\n%q\nwant\n%q", name, back, src)
		}
	}
	if _, ok := tomlTopLevelInt("[t]\nmodel_auto_compact_token_limit = 5\n", codexKey); ok {
		t.Error("a key inside a table was read as top level")
	}
}

// A file something else edited after tokenops is reverted key by key:
// tokenops' key goes, the other edit stays.
func TestRevertKeepsOtherEditsToTheFile(t *testing.T) {
	p := paths(t)
	write(t, p.ClaudeSettings, `{"model":"opus"}`)
	if _, err := Apply(p, Plan{ClaudeCompactAt: 600_000}, now); err != nil {
		t.Fatal(err)
	}
	m := jsonOf(t, p.ClaudeSettings)
	m["theme"] = "dark"
	b, _ := json.Marshal(m)
	write(t, p.ClaudeSettings, string(b))
	rs, err := Revert(p)
	if err != nil || len(rs) != 1 || rs[0].Status != StatusReverted {
		t.Fatalf("revert = %+v, %v", rs, err)
	}
	got := jsonOf(t, p.ClaudeSettings)
	if _, still := got["env"]; still || got["theme"] != "dark" || got["model"] != "opus" {
		t.Errorf("after revert = %v; want only tokenops' key removed", got)
	}
}

// An operator's own autoCompactWindow, or their own env value, keeps
// tokenops out of Claude Code: its env variable would override theirs.
func TestClaudeOperatorWindowIsRespected(t *testing.T) {
	for name, body := range map[string]string{
		"setting": `{"autoCompactWindow":800000}`,
		"env":     `{"env":{"CLAUDE_CODE_AUTO_COMPACT_WINDOW":"700000","OTHER":"x"}}`,
	} {
		p := paths(t)
		write(t, p.ClaudeSettings, body)
		rs, err := Apply(p, Plan{ClaudeCompactAt: 600_000}, now)
		if err != nil {
			t.Fatal(err)
		}
		if c := byClient(rs, ClientClaudeCode); len(c) != 1 || c[0].Status != StatusYours {
			t.Errorf("%s: %+v; want yours", name, c)
		}
		if read(t, p.ClaudeSettings) != body {
			t.Errorf("%s: settings changed", name)
		}
	}
}

// Keeping the operator's other env variables: tokenops adds its own to
// the block and, reverting key by key, removes only its own.
func TestClaudeEnvBlockIsShared(t *testing.T) {
	p := paths(t)
	write(t, p.ClaudeSettings, `{"env":{"FOO":"1"}}`)
	if _, err := Apply(p, Plan{ClaudeCompactAt: 600_000}, now); err != nil {
		t.Fatal(err)
	}
	env := jsonOf(t, p.ClaudeSettings)["env"].(map[string]any)
	if env["FOO"] != "1" || env["CLAUDE_CODE_AUTO_COMPACT_WINDOW"] != "633000" {
		t.Fatalf("env = %v", env)
	}
	m := jsonOf(t, p.ClaudeSettings)
	m["theme"] = "dark" // force the key-by-key path
	b, _ := json.Marshal(m)
	write(t, p.ClaudeSettings, string(b))
	if _, err := Revert(p); err != nil {
		t.Fatal(err)
	}
	if env := jsonOf(t, p.ClaudeSettings)["env"].(map[string]any); len(env) != 1 || env["FOO"] != "1" {
		t.Errorf("env after revert = %v; want only the operator's FOO", env)
	}
}
