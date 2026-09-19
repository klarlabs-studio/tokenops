package daemon

import (
	"errors"
	"strings"
	"testing"
)

func TestRestartForConfigRestartsASupervisedDaemon(t *testing.T) {
	called := false
	r := restartForConfig(true, func() error { called = true; return nil })
	if !called || !r.Restarted {
		t.Errorf("supervised daemon not restarted: called=%v %+v", called, r)
	}
	if !strings.Contains(r.Note(), "live") {
		t.Errorf("note = %q", r.Note())
	}
}

// Nothing supervised means nothing to bounce, and the note must not name
// `tokenops start` as the fix on a machine where it would start a second
// daemon — the old MCP hint did exactly that.
func TestRestartForConfigDoesNotClaimARestartThatDidNotHappen(t *testing.T) {
	r := restartForConfig(false, func() error { t.Fatal("restarted an unsupervised daemon"); return nil })
	if r.Restarted {
		t.Error("reported a restart")
	}
	failed := restartForConfig(true, func() error { return errors.New("boom") })
	if failed.Restarted || !strings.Contains(failed.Note(), "tokenops daemon restart") {
		t.Errorf("failed restart note = %q", failed.Note())
	}
}
