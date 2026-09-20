package cli

import (
	"bytes"
	"strings"
	"testing"
)

// `tokenops mode` printed the daemon's rung and a count of budgets and
// routing rules. That answers "which of the five settings did I last
// touch", not "what can TokenOps do without me" — and the other four
// settings, in three other vocabularies, were nowhere.
func TestModeReportsEveryControlSurface(t *testing.T) {
	path := seedConfig(t)
	var buf bytes.Buffer
	cmd := newModeCmd()
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"--config-path", path})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("mode: %v", err)
	}

	out := buf.String()
	for _, want := range []string{"coaching", "smart_routing", "read_guard", "routing_approval"} {
		if !strings.Contains(out, want) {
			t.Errorf("mode output omits %q:\n%s", want, out)
		}
	}
}

// The headline is the question, not the setting. An operator should not
// have to read a table to learn whether anything is acting on its own.
func TestModeSaysPlainlyWhetherAnythingActs(t *testing.T) {
	path := seedConfig(t)
	var buf bytes.Buffer
	cmd := newModeCmd()
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"--config-path", path})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("mode: %v", err)
	}
	if !strings.Contains(buf.String(), "without asking") {
		t.Errorf("nothing states what TokenOps may do unasked:\n%s", buf.String())
	}
}

// A subsystem the daemon is holding back must say so, or turning the
// daemon up will surprise whoever does it.
func TestModeNamesWhatIsHeldBack(t *testing.T) {
	path := seedConfig(t)
	cfg, err := readMutableConfig(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	cfg.Mode = "passive"
	cfg.Coaching.Delivery = "intervene"
	if err := writeMutableConfig(path, cfg); err != nil {
		t.Fatalf("write: %v", err)
	}

	var buf bytes.Buffer
	cmd := newModeCmd()
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"--config-path", path})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("mode: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "held back") {
		t.Errorf("a capped subsystem was not flagged:\n%s", out)
	}
}
