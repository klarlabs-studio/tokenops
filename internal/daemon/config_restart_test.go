package daemon

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRestartForConfigRestartsASupervisedDaemon(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(cfg, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name           string
		reads, written string
	}{
		{"the daemon reads the file written", cfg, cfg},
		{"the same file, written another way", cfg, filepath.Join(dir, "sub", "..", "config.yaml")},
		{"the unit does not say what it reads", "", cfg},
		{"the writer does not say what it wrote", cfg, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			called := false
			r := restartForConfig(true, tt.reads, tt.written, func() error { called = true; return nil })
			if !called || !r.Restarted {
				t.Errorf("supervised daemon not restarted: called=%v %+v", called, r)
			}
			if !strings.Contains(r.Note(), "live") {
				t.Errorf("note = %q", r.Note())
			}
			if _, will := planConfigRestart(true, tt.reads, tt.written); !will {
				t.Error("the plan says it would not restart")
			}
		})
	}
}

// Nothing supervised means nothing to bounce, and the note must not name
// `tokenops start` as the fix on a machine where it would start a second
// daemon — the old MCP hint did exactly that.
func TestRestartForConfigDoesNotClaimARestartThatDidNotHappen(t *testing.T) {
	r := restartForConfig(false, "", "/x", func() error { t.Fatal("restarted an unsupervised daemon"); return nil })
	if r.Restarted {
		t.Error("reported a restart")
	}
	failed := restartForConfig(true, "", "/x", func() error { return errors.New("boom") })
	if failed.Restarted || !strings.Contains(failed.Note(), "tokenops daemon restart") {
		t.Errorf("failed restart note = %q", failed.Note())
	}
}

// The daemon reads the config its unit gives it. A write to another file
// followed by a restart brought the daemon back with the config it had,
// and the command said the change was live.
func TestRestartForConfigLeavesADaemonThatReadsAnotherFile(t *testing.T) {
	reads, written := "/home/ada/.config/tokenops/config.yaml", "/data/cfg/tokenops/config.yaml"
	r := restartForConfig(true, reads, written, func() error {
		t.Fatal("restarted a daemon that does not read the file written")
		return nil
	})
	if r.Restarted || r.Err != nil || !r.Supervised {
		t.Errorf("outcome = %+v", r)
	}
	note := r.Note()
	for _, want := range []string{reads, written, "not live", "tokenops daemon install --config " + written} {
		if !strings.Contains(note, want) {
			t.Errorf("note does not say %q: %s", want, note)
		}
	}
	if strings.Contains(note, "the change is live") {
		t.Errorf("note claims the change is live: %s", note)
	}
	planned, will := planConfigRestart(true, reads, written)
	if will || planned.Note() != note {
		t.Errorf("the plan says will=%v %q, want the same refusal", will, planned.Note())
	}
}
