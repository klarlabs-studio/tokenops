package state

import (
	"strings"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/observability/freshness"
)

// The daemon's assessment and the warning it is worded into must agree on
// the grade. The warning recomputes severity from the gap, so a report whose
// origin could not be read has to carry that fact through, or the daemon
// grades it a warning and `tokenops status` prints it as critical.
func TestSourceWarningsKeepAnUnreadableOriginAtWarning(t *testing.T) {
	tests := []struct {
		name          string
		originUnknown bool
		wantGrade     string
	}{
		{"origin unreadable", true, "[warning]"},
		{"origin readable and ahead", false, "[critical]"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := SourceReport{
				Name: "opencode", Tag: "opencode",
				State:         freshness.StateStale,
				LastEventAt:   time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC),
				SilentFor:     43 * 24 * time.Hour,
				OriginUnknown: tt.originUnknown,
				Window:        48 * time.Hour,
			}
			got := SourceWarnings([]SourceReport{r})
			if len(got.Stale) != 1 {
				t.Fatalf("stale = %v, want one warning", got.Stale)
			}
			if !strings.Contains(got.Stale[0], tt.wantGrade) {
				t.Errorf("warning %q is not graded %s", got.Stale[0], tt.wantGrade)
			}
		})
	}
}
