package modellever

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func paths(t *testing.T, settings string) Paths {
	t.Helper()
	home := t.TempDir()
	p := Paths{ClaudeSettings: filepath.Join(home, ".claude", "settings.json"), State: filepath.Join(home, ".tokenops", "coach", "default-model.json")}
	if err := os.MkdirAll(filepath.Dir(p.ClaudeSettings), 0o700); err != nil {
		t.Fatal(err)
	}
	if settings != "" {
		if err := os.WriteFile(p.ClaudeSettings, []byte(settings), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return p
}

func settings(t *testing.T, p Paths) map[string]any {
	t.Helper()
	b, err := os.ReadFile(p.ClaudeSettings)
	if err != nil {
		t.Fatal(err)
	}
	m := map[string]any{}
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

var now = time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)

// Applying sets the key, keeps every other setting, and is idempotent;
// reverting removes exactly what was added.
func TestApplyThenRevert(t *testing.T) {
	p := paths(t, `{"env":{"FOO":"1"},"hooks":{}}`)
	want := Setting{Value: "sonnet", From: "claude-opus-5", Reason: "weekly runs out early"}
	r, err := Apply(p, want, now)
	if err != nil || r.Status != StatusApplied {
		t.Fatalf("apply: %+v %v", r, err)
	}
	if m := settings(t, p); m["model"] != "sonnet" || m["env"] == nil || m["hooks"] == nil {
		t.Fatalf("settings after apply: %v", m)
	}
	if r, _ := Apply(p, want, now); r.Status != StatusActive {
		t.Errorf("second apply: %s", r.Status)
	}
	if s, r := Current(p); s == nil || r.Status != StatusActive || s.From != "claude-opus-5" {
		t.Errorf("current: %+v %+v", s, r)
	}
	r, err = Revert(p)
	if err != nil || r.Status != StatusReverted {
		t.Fatalf("revert: %+v %v", r, err)
	}
	if m := settings(t, p); m["model"] != nil || m["env"] == nil {
		t.Errorf("settings after revert: %v", m)
	}
	if r, _ := Revert(p); r.Status != StatusNone {
		t.Errorf("second revert: %s", r.Status)
	}
}

// A model the operator set is theirs: never overwritten, never removed.
func TestOperatorsModelIsLeftAlone(t *testing.T) {
	p := paths(t, `{"model":"opus"}`)
	if r, _ := Apply(p, Setting{Value: "sonnet"}, now); r.Status != StatusYours || r.Model != "opus" {
		t.Fatalf("apply over the operator's model: %+v", r)
	}
	if m := settings(t, p); m["model"] != "opus" {
		t.Errorf("model = %v", m["model"])
	}
	if r, _ := Revert(p); r.Status != StatusNone {
		t.Errorf("revert of nothing of ours: %s", r.Status)
	}
}

// A /model choice after tokenops set the key makes it the operator's:
// neither re-applied nor reverted.
func TestChangedValueBecomesTheOperators(t *testing.T) {
	p := paths(t, `{}`)
	if _, err := Apply(p, Setting{Value: "sonnet"}, now); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p.ClaudeSettings, []byte(`{"model":"opus"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, r := Current(p); r.Status != StatusEdited {
		t.Errorf("current: %s", r.Status)
	}
	if r, _ := Revert(p); r.Status != StatusEdited {
		t.Errorf("revert: %s", r.Status)
	}
	if m := settings(t, p); m["model"] != "opus" {
		t.Errorf("model = %v", m["model"])
	}
	if r, _ := Apply(p, Setting{Value: "sonnet"}, now); r.Status != StatusYours {
		t.Errorf("apply after the operator chose: %s", r.Status)
	}
}

// Without Claude Code installed nothing is written; a file that is not
// plain JSON is left alone.
func TestUnavailable(t *testing.T) {
	p := Paths{ClaudeSettings: filepath.Join(t.TempDir(), "absent", "settings.json"), State: filepath.Join(t.TempDir(), "s.json")}
	if r, _ := Apply(p, Setting{Value: "sonnet"}, now); r.Status != StatusUnavailable {
		t.Errorf("no Claude Code: %s", r.Status)
	}
	q := paths(t, `{"model": // comment`)
	if r, err := Apply(q, Setting{Value: "sonnet"}, now); r.Status != StatusUnavailable || err == nil {
		t.Errorf("not JSON: %+v %v", r, err)
	}
}

const formatted = "{\n  \"zeta\": 1,\n  \"hooks\": {\n    \"Stop\": [{\"command\": \"a && b 2>/dev/null; echo '<x>'\"}]\n  },\n  \"alpha\": 633000\n}\n"

// Setting and reverting the model touches only the model line: the
// operator's key order and hook commands (with && and >) stay readable.
func TestApplyAndRevertKeepTheOperatorsFormatting(t *testing.T) {
	p := paths(t, formatted)
	if _, err := Apply(p, Setting{Value: "sonnet"}, now); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p.ClaudeSettings)
	want := formatted[:len(formatted)-3] + ",\n  \"model\": \"sonnet\"\n}\n"
	if string(b) != want {
		t.Fatalf("after apply:\n%s\nwant:\n%s", b, want)
	}
	if _, err := Revert(p); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(p.ClaudeSettings)
	if string(b) != formatted {
		t.Fatalf("after revert:\n%s", b)
	}
}
