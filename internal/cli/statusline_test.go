package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The JSON Claude Code documents for a statusLine command.
const claudeStatusSample = `{
  "session_id": "abc", "model": {"id": "claude-opus-5-5", "display_name": "Opus 5.5"},
  "effort": {"level": "high"},
  "cost": {"total_cost_usd": 3.5},
  "context_window": {"used_percentage": 42.5, "context_window_size": 1000000},
  "prompt_cache": {"hit_ratio": 0.9},
  "rate_limits": {"five_hour": {"used_percentage": 23.5, "resets_at": 4102444800},
                  "seven_day": {"used_percentage": 41.2, "resets_at": 4102444800}}
}`

func TestStatuslineRendersClaudeCodesInput(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	lines := statusLines([]byte(claudeStatusSample))
	if len(lines) == 0 {
		t.Fatal("no line")
	}
	for _, want := range []string{"Opus 5.5 high", "5h 24%", "wk 41%", "ctx 42%", "cache 90%"} {
		if !strings.Contains(lines[0], want) {
			t.Errorf("line %q lacks %q", lines[0], want)
		}
	}
	if statusLines([]byte("not json")) != nil {
		t.Error("garbage input produced a line; it must fail open with nothing")
	}
}

func TestStatuslineWrapsAnotherCommand(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	var out bytes.Buffer
	root := NewRoot()
	root.SetArgs([]string{"statusline", "--wrap", "printf 'mine:%s' \"$(cat | head -c 1)\""})
	root.SetIn(strings.NewReader(claudeStatusSample))
	root.SetOut(&out)
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	got := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(got) != 2 || got[1] != "mine:{" {
		t.Errorf("output %q; want TokenOps' line, then the wrapped command's with the same input", got)
	}
}

// Installing keeps the operator's status line, wrapped; uninstalling puts
// it back exactly.
func TestStatuslineInstallKeepsAndRestoresTheOriginal(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	settings := filepath.Join(home, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settings), 0o755); err != nil {
		t.Fatal(err)
	}
	original := `{"statusLine":{"type":"command","command":"printf '%s' \"$(hostname -s)\"","padding":1},"other":true}`
	if err := os.WriteFile(settings, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := installStatusline(&out, settings, "/opt/bin/tokenops"); err != nil {
		t.Fatal(err)
	}
	sl := readStatusLine(t, settings)
	command, _ := sl["command"].(string)
	if !strings.HasPrefix(command, "/opt/bin/tokenops statusline --wrap ") || !strings.Contains(command, "hostname") {
		t.Errorf("command %q", command)
	}
	if sl["padding"] != float64(1) || sl["refreshInterval"] != float64(statuslineRefreshSeconds) {
		t.Errorf("statusLine %v", sl)
	}
	// Idempotent.
	out.Reset()
	if err := installStatusline(&out, settings, "/opt/bin/tokenops"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Nothing changed") {
		t.Errorf("second install: %s", out.String())
	}
	if err := uninstallStatusline(&out, settings); err != nil {
		t.Fatal(err)
	}
	if sl := readStatusLine(t, settings); sl["command"] != `printf '%s' "$(hostname -s)"` || sl["refreshInterval"] != nil {
		t.Errorf("restored %v", sl)
	}
	b, _ := os.ReadFile(settings)
	if strings.Contains(string(b), "subagentStatusLine") {
		t.Error("TokenOps' subagent rows were left behind")
	}
}

func readStatusLine(t *testing.T, path string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var s map[string]any
	if err := json.Unmarshal(b, &s); err != nil {
		t.Fatal(err)
	}
	sl, _ := s["statusLine"].(map[string]any)
	return sl
}

func TestStatuslineSubagentRows(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	var out bytes.Buffer
	root := NewRoot()
	root.SetArgs([]string{"statusline", "subagents"})
	root.SetIn(strings.NewReader(`{"columns":80,"tasks":[{"id":"t1","name":"Explore","model":"claude-haiku-4-5-20251001","effort":"low","contextWindowSize":200000,"tokenCount":60000},{"id":"t2","name":"plan"}]}`))
	root.SetOut(&out)
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 2 || !strings.Contains(lines[0], `"id":"t1"`) || !strings.Contains(lines[0], "haiku-4-5 low · ▰▱▱▱ 30%") {
		t.Errorf("rows %q", lines)
	}
}

// An uninstall is the operator's choice: a later init leaves the status
// line out, and only an explicit install brings it back.
func TestStatuslineUninstallSticksAcrossInit(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	cfgPath := filepath.Join(home, ".config", "tokenops", "config.yaml")
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfgPath, []byte("listen: 127.0.0.1:7878\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	settings := filepath.Join(home, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settings), 0o755); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) string {
		var out bytes.Buffer
		root := NewRoot()
		root.SetArgs(append([]string{"statusline"}, args...))
		root.SetOut(&out)
		root.SetErr(&out)
		if err := root.Execute(); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out.String())
		}
		return out.String()
	}
	if step := statuslineStep(cfgPath, settings, "/opt/bin/tokenops"); readStatusLine(t, settings) == nil {
		t.Fatalf("init did not install by default: %+v", step)
	}
	if out := run("uninstall", "--settings", settings); !strings.Contains(out, "init will leave it out") {
		t.Errorf("uninstall: %s", out)
	}
	step := statuslineStep(cfgPath, settings, "/opt/bin/tokenops")
	if readStatusLine(t, settings) != nil || !strings.Contains(step.Detail, "you uninstalled it") {
		t.Fatalf("init put an uninstalled status line back: %+v", step)
	}
	run("install", "--settings", settings)
	if readStatusLine(t, settings) == nil {
		t.Fatal("install did not bring it back")
	}
	cfg, err := readMutableConfig(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.StatuslineWanted() {
		t.Error("an explicit install left init's opt-out in place")
	}
}

func TestStatuslineNameIsReadable(t *testing.T) {
	for cmd, want := range map[string]string{
		"/Users/x/.nvm/versions/node/v25.6.0/bin/node /Users/x/.fireconnect/cli/packages/setup-cli/bin/claude-statusline.mjs": "Your FireConnect status line",
		"~/.claude/statusline.sh": "Your status line (statusline.sh)",
		`printf '%s' "$(pwd)"`:    "Your previous status line",
	} {
		if got := statuslineName(cmd); got != want {
			t.Errorf("statuslineName(%q) = %q, want %q", cmd, got, want)
		}
	}
}
