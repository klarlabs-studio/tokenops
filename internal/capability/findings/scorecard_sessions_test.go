package findings

import (
	"strings"
	"testing"
	"time"
)

// The scorecard's session quality is the daemon's DX grade of the last
// seven days; another period, a stale analysis or no graded instruction
// says why there is none.
func TestScorecardSessions(t *testing.T) {
	now := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
	dir := t.TempDir()
	if g := scorecardSessions(dir, 7, now); g.Letter != "" || !strings.Contains(g.Missing, "not analysed") {
		t.Errorf("no analysis: %+v", g)
	}
	snap := SessionsSnapshot{ComputedAt: now.Add(-time.Hour)}
	snap.DX.Metrics.Prompts = 412
	snap.DX.Grades.Overall, snap.DX.Grades.OverallDriver = "C", "rework"
	if _, err := WriteSnapshot(dir, snap); err != nil {
		t.Fatal(err)
	}
	if g := scorecardSessions(dir, 7, now); g.Letter != "C" || g.Driver != "rework" || g.Instructions != 412 || g.Missing != "" {
		t.Errorf("fresh analysis: %+v", g)
	}
	if g := scorecardSessions(dir, 0, now); g.Letter != "C" {
		t.Errorf("the default period is seven days: %+v", g)
	}
	if g := scorecardSessions(dir, 30, now); g.Letter != "" || !strings.Contains(g.Missing, "`tokenops dx --days 30`") {
		t.Errorf("30 days: %+v", g)
	}
	if g := scorecardSessions(dir, 7, now.Add(MaxSnapshotAge+time.Hour)); g.Letter != "" {
		t.Errorf("stale analysis graded: %+v", g)
	}
	snap.DX.Grades.Overall = ""
	if _, err := WriteSnapshot(dir, snap); err != nil {
		t.Fatal(err)
	}
	if g := scorecardSessions(dir, 7, now); g.Letter != "" || !strings.Contains(g.Missing, "could be graded") {
		t.Errorf("nothing graded: %+v", g)
	}
}
