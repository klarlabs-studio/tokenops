package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	coachcap "go.klarlabs.de/tokenops/internal/capability/coach"
	"go.klarlabs.de/tokenops/internal/config"
	ft "go.klarlabs.de/tokenops/internal/contexts/coaching/followthrough"
	"go.klarlabs.de/tokenops/internal/infra/followthrough"
)

func TestCoachTipAnswers(t *testing.T) {
	l, err := followthrough.Default()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(l.Path) })
	_ = os.Remove(l.Path)

	out, err := runCoachCmd(t, "tip", "ack")
	if err != nil || !strings.Contains(out, "No tip to answer") {
		t.Fatalf("empty ledger: %v %q", err, out)
	}

	now := time.Now()
	coachcap.RecordTip(l, now.Add(-2*time.Minute), "t1", "s", "budget_50", coachcap.ViaStopHook)
	coachcap.RecordTip(l, now.Add(-time.Minute), "t2", "s", "budget_75", coachcap.ViaStopHook)

	out, err = runCoachCmd(t, "tip", "expected")
	if err != nil || !strings.Contains(out, "at 75% of the session budget as expected") {
		t.Fatalf("expected: %v %q", err, out)
	}
	out, err = runCoachCmd(t, "tip", "snooze", "--for", "2h")
	if err != nil || !strings.Contains(out, "Snoozed the tip at 50% of the session budget") {
		t.Fatalf("snooze: %v %q", err, out)
	}
	if _, err := runCoachCmd(t, "tip", "maybe"); err == nil {
		t.Fatal("an unknown answer was accepted")
	}

	entries, _ := l.Load()
	recs := ft.Fold(entries, now)
	if recs[0].State() != ft.StateSnoozed || recs[1].State() != ft.StateExpected {
		t.Fatalf("states = %s, %s", recs[0].State(), recs[1].State())
	}

	path, err := config.DefaultPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("coach:\n  autonomy: advise\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(path) })
	out, err = runCoachCmd(t)
	if err != nil || !strings.Contains(out, "1 snoozed (100% snoozed)") {
		t.Fatalf("follow-through does not report the answers: %v\n%s", err, out)
	}
}
