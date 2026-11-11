package spending

import (
	"context"
	"testing"
	"time"
)

// The session grade a caller hands over is the scorecard's session
// quality, and with nothing else measured it is the grade.
func TestScorecardGradesTheSessionsItIsGiven(t *testing.T) {
	now := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
	s := Scorecard(context.Background(), nil, ScorecardParams{TranscriptRoot: t.TempDir(), Now: now,
		Sessions: &ScorecardSessions{Letter: "B", Driver: "rework", Instructions: 80}})
	if s.Sessions.Grade != "B" || s.OverallGrade != "B" || s.OverallDriver != "session quality" {
		t.Errorf("sessions %+v, overall %q from %q", s.Sessions, s.OverallGrade, s.OverallDriver)
	}
}
