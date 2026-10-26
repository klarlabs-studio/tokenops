package cli

import (
	"strings"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/findings"
)

// The CLI lists every finding and says which the menu bar hides.
func TestGlanceFindingsNoteTheHiddenOnes(t *testing.T) {
	now := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	r := findings.Report{Findings: []findings.Finding{
		{Key: "waste:week", Level: findings.LevelInfo}, {Key: "coach:observing", Level: findings.LevelInfo},
	}}
	if got := hiddenNote(r, nil, now); got != "" {
		t.Errorf("nothing hidden = %q", got)
	}
	ds := []findings.Dismissal{{Key: "waste:week", Level: findings.LevelInfo, Until: now.Add(time.Hour)}}
	if got := hiddenNote(r, ds, now); !strings.Contains(got, "1 of these is hidden in the menu bar") {
		t.Errorf("one hidden = %q", got)
	}
}
