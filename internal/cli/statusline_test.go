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
	if !strings.Contains(out.String(), "no changes") {
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
