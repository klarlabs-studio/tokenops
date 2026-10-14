package findings

import (
	"strings"
	"testing"

	"go.klarlabs.de/tokenops/internal/capability/coach"
	ft "go.klarlabs.de/tokenops/internal/contexts/coaching/followthrough"
	"go.klarlabs.de/tokenops/internal/infra/coachhook"
)

// Tips held for the status line, and tips the operator keeps snoozing,
// both show where the operator looks: glance's findings.
func TestLifecycleFindings(t *testing.T) {
	c := &coach.Report{FollowThrough: []ft.Summary{
		{Power: "inform", Channel: ft.ChannelTip, Kind: "budget_50", Snoozed: 4, Followed: 1, Expected: 1},
		{Power: "inform", Channel: ft.ChannelTip, Kind: "quota_75", Snoozed: 1, Followed: 5},
	}}
	s := &coachhook.Stats{Suppressed: map[string]int{coachhook.SuppressedDailyBudget: 7}}
	got := coaching(c, s)
	titles := make([]string, 0, len(got))
	for _, f := range got {
		titles = append(titles, f.Title)
	}
	joined := strings.Join(titles, " | ")
	for _, want := range []string{
		"7 tips waited on the status line instead of interrupting",
		"You snooze most session budget tips",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %q in %q", want, joined)
		}
	}
	if strings.Contains(joined, "quota") {
		t.Errorf("a rarely snoozed kind was reported: %q", joined)
	}
}
