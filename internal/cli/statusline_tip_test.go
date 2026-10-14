package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// statuslineHome isolates a status line run: its own HOME and config,
// with the coach-hook state for session "abc" holding tip.
func statuslineHome(t *testing.T, configYAML string, tip map[string]any) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("NO_COLOR", "1")
	if configYAML != "" {
		path := filepath.Join(home, ".config", "tokenops", "config.yaml")
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(configYAML), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if tip != nil {
		dir := filepath.Join(home, ".tokenops", "coach-hook")
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		b, _ := json.Marshal(map[string]any{"open_tip": tip})
		if err := os.WriteFile(filepath.Join(dir, "session-abc.json"), b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return home
}

// seenEntries reads the follow-through ledger's seen marks.
func seenEntries(t *testing.T, home string) []map[string]any {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(home, ".tokenops", "coach", "followthrough.jsonl"))
	if err != nil {
		return nil
	}
	var out []map[string]any
	for line := range strings.SplitSeq(strings.TrimSpace(string(b)), "\n") {
		var e map[string]any
		if json.Unmarshal([]byte(line), &e) == nil && e["type"] == "seen" {
			out = append(out, e)
		}
	}
	return out
}

func quotaTip(kind string) map[string]any {
	return map[string]any{
		"id": "tip-1", "kind": kind, "window": "Claude weekly",
		"resets_at": time.Now().Add(94 * time.Hour).UTC().Format(time.RFC3339),
	}
}

// The open tip ends the line the operator reads every turn, and the
// first showing is recorded as seen, once, however often the line
// refreshes.
func TestStatuslineShowsTheOpenTipAndRecordsItSeen(t *testing.T) {
	home := statuslineHome(t, "", quotaTip("quota_75"))
	var lines []string
	for range 3 {
		lines = statusLines([]byte(claudeStatusSample))
	}
	if len(lines) != 1 || !strings.Contains(lines[0], "● Claude weekly 75% used → /compact, smaller model") {
		t.Fatalf("lines %q", lines)
	}
	seen := seenEntries(t, home)
	if len(seen) != 1 || seen[0]["id"] != "tip-1" || seen[0]["via"] != "statusline" {
		t.Fatalf("seen entries = %+v; want one for tip-1 via the status line", seen)
	}
}

func TestStatuslineTipRespectsTheCoach(t *testing.T) {
	cases := []struct {
		name, config, kind string
		shown              bool
	}{
		{"normal shows an early tip", "", "quota_75", true},
		{"quiet holds back an early tip", "coach:\n  verbosity: quiet\n", "quota_75", false},
		{"quiet holds back compact", "coach:\n  verbosity: quiet\n", "compact_now", false},
		{"quiet still shows work about to stop", "coach:\n  verbosity: quiet\n", "quota_90", true},
		{"inform off shows nothing", "coach:\n  autonomy: off\n", "quota_90", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			home := statuslineHome(t, c.config, quotaTip(c.kind))
			lines := statusLines([]byte(claudeStatusSample))
			if got := strings.Contains(lines[0], "●"); got != c.shown {
				t.Fatalf("tip shown = %v; want %v: %q", got, c.shown, lines[0])
			}
			if got := len(seenEntries(t, home)) == 1; got != c.shown {
				t.Errorf("recorded seen = %v; want %v", got, c.shown)
			}
		})
	}
}

// A different session, or a session with no open tip, shows none.
func TestStatuslineShowsOnlyThisSessionsTip(t *testing.T) {
	home := statuslineHome(t, "", quotaTip("quota_75"))
	other := strings.Replace(claudeStatusSample, `"session_id": "abc"`, `"session_id": "xyz"`, 1)
	if lines := statusLines([]byte(other)); strings.Contains(lines[0], "●") {
		t.Fatalf("another session's tip shown: %q", lines[0])
	}
	if len(seenEntries(t, home)) != 0 {
		t.Error("a tip never shown was recorded as seen")
	}
}
