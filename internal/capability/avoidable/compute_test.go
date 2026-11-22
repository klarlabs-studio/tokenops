package avoidable

import (
	"slices"
	"testing"
	"time"
	"unsafe"

	"go.klarlabs.de/tokenops/internal/contexts/observability/analytics"
)

// The report keeps the turns before its end, in order, and up to now
// (every turn before it) it keeps the turns it was handed rather than a
// copy of a month of them.
func TestTurnsBefore(t *testing.T) {
	now := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	turns := []analytics.SessionTurn{
		{SessionID: "a", At: now.Add(-3 * time.Hour)},
		{SessionID: "b", At: now.Add(-time.Hour)},
		{SessionID: "c", At: now.Add(-2 * time.Hour)},
	}
	for _, tc := range []struct {
		name  string
		until time.Time
		want  []string
		same  bool
	}{
		{"all before", now, []string{"a", "b", "c"}, true},
		{"some at or after", now.Add(-time.Hour), []string{"a", "c"}, false},
		{"none before", now.Add(-5 * time.Hour), nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := turnsBefore(turns, tc.until)
			var ids []string
			for _, x := range got {
				ids = append(ids, x.SessionID)
			}
			if !slices.Equal(ids, tc.want) {
				t.Fatalf("turns %v; want %v", ids, tc.want)
			}
			if same := len(got) > 0 && unsafe.SliceData(got) == unsafe.SliceData(turns); same != tc.same {
				t.Errorf("shares the turns handed in: %v; want %v", same, tc.same)
			}
		})
	}
}
