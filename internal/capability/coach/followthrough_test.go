package coach

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/config"
	ft "go.klarlabs.de/tokenops/internal/contexts/coaching/followthrough"
)

type memLedger struct {
	entries []ft.Entry
	loadErr error
}

func (m *memLedger) Append(es ...ft.Entry) error { m.entries = append(m.entries, es...); return nil }
func (m *memLedger) Load() ([]ft.Entry, error)   { return m.entries, m.loadErr }

var now = time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestApplyRecordsOnlyLoweredPowers(t *testing.T) {
	path := writeConfig(t, "coach:\n  autonomy: autonomous\n")
	l := &memLedger{}
	r, err := Apply(path, l, now, func(c *config.Config) { c.Coach.Autonomy = config.AutonomyAdvise })
	if err != nil {
		t.Fatal(err)
	}
	if r.Effective(config.PowerModels) != config.AutonomyAdvise {
		t.Fatalf("Apply returned %+v", r)
	}
	// waste and models were effectively autonomous; inform was advise
	// (its ask/autonomous rungs deliver as advise), so it did not fall.
	powers := make([]string, 0, len(l.entries))
	for _, e := range l.entries {
		if e.Type != ft.EntryLowered || e.From != config.AutonomyAutonomous || e.To != config.AutonomyAdvise {
			t.Errorf("unexpected entry %+v", e)
		}
		powers = append(powers, e.Power)
	}
	if fmt.Sprint(powers) != "[waste models]" {
		t.Errorf("lowered powers = %v; want [waste models]", powers)
	}
	l.entries = nil
	if _, err := Apply(path, l, now, func(c *config.Config) { c.Coach.Autonomy = config.AutonomyAutonomous }); err != nil {
		t.Fatal(err)
	}
	if len(l.entries) != 0 {
		t.Errorf("raising recorded %+v", l.entries)
	}
}

func TestApplyRejectsInvalidSettingsWithoutWriting(t *testing.T) {
	path := writeConfig(t, "coach:\n  autonomy: advise\n")
	before, _ := os.ReadFile(path)
	l := &memLedger{}
	if _, err := Apply(path, l, now, func(c *config.Config) { c.Coach.Autonomy = "sometimes" }); err == nil {
		t.Fatal("invalid rung accepted")
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) || len(l.entries) != 0 {
		t.Error("an invalid change was written or recorded")
	}
}

func TestMovesAndAdviceFoldIntoStatus(t *testing.T) {
	l := &memLedger{}
	RecordMove(l, now.Add(-48*time.Hour), "m1", "s", config.PowerModels, "lookup", "claude-opus-5", "claude-haiku-4-5")
	for i := range ft.QuietAfter {
		id := fmt.Sprint("a", i)
		RecordAdvice(l, now.Add(-time.Hour), id, "s", config.PowerModels, "lookup", "claude-opus-5", "claude-haiku-4-5")
		RecordResolutions(l, now, []Resolution{{ID: id, Kind: "lookup"}})
	}
	if CountMoves(l, config.PowerModels) != 1 {
		t.Errorf("CountMoves = %d", CountMoves(l, config.PowerModels))
	}
	if !Quieter(l, now)(config.PowerModels, "lookup") {
		t.Error("repeatedly ignored advice not quieted")
	}
	r := Status(config.Config{}, l, now)
	if len(r.FollowThrough) != 2 {
		t.Fatalf("FollowThrough = %+v", r.FollowThrough)
	}
}

// A ledger that cannot be read must not silence the coach.
func TestUnreadableLedgerQuietsNothing(t *testing.T) {
	l := &memLedger{loadErr: errors.New("disk")}
	if Quieter(l, now)(config.PowerModels, "lookup") || Quieter(nil, now)(config.PowerModels, "lookup") {
		t.Error("quieted without evidence")
	}
}

func TestCompactAtFollowsTheFindingsLine(t *testing.T) {
	var c config.Config
	if got := CompactAt(c, "claude-code:"); got != 600_000 {
		t.Errorf("claude-code default = %d; want 600000", got)
	}
	if got := CompactAt(c, "codex:"); got != 150_000 {
		t.Errorf("codex default = %d; want 150000", got)
	}
	c.Coaching.ContextLimits = []config.ContextLimitConfig{{WorkflowPrefix: "claude-code:", CompactAtTokens: 400_000}}
	if got := CompactAt(c, "claude-code:"); got != 400_000 {
		t.Errorf("override = %d; want 400000", got)
	}
}
