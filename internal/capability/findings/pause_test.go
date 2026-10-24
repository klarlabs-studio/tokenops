package findings

import (
	"strings"
	"testing"

	"go.klarlabs.de/tokenops/internal/capability/boundaries"
	"go.klarlabs.de/tokenops/internal/capability/headroom"
)

func pauseFindings(r Report) []Finding {
	var out []Finding
	for _, f := range r.Findings {
		if f.Kind == KindBoundary {
			out = append(out, f)
		}
	}
	return out
}

func TestHeadroomPauseFindings(t *testing.T) {
	for _, tc := range []struct {
		name   string
		glance *headroom.Glance
		pause  *boundaries.PauseSummary
		// want is a phrase the one finding holds; empty wants none.
		want, action string
	}{
		{name: "absent with a plan to watch suggests it", glance: glance(), pause: &boundaries.PauseSummary{},
			want: "No headroom pause", action: "tokenops boundaries headroom-pause"},
		{name: "absent with no plan says nothing", glance: &headroom.Glance{}, pause: &boundaries.PauseSummary{}},
		{name: "unknown says nothing", glance: glance()},
		{name: "in shadow with nothing to show says nothing", glance: glance(),
			pause: &boundaries.PauseSummary{Configured: true, Rule: "headroom-pause", Stage: "shadow", PausePoint: true}},
		{name: "in shadow shows what it would have paused", glance: glance(),
			pause: &boundaries.PauseSummary{Configured: true, Rule: "headroom-pause", Stage: "shadow", Sessions: 3, PausePoint: true},
			want:  "would have paused 3 sessions this week", action: "tokenops boundaries promote headroom-pause"},
		{name: "one session reads as one", glance: glance(),
			pause: &boundaries.PauseSummary{Configured: true, Rule: "headroom-pause", Stage: "shadow", Sessions: 1, PausePoint: true},
			want:  "would have paused 1 session this week"},
		{name: "enforced shows what it paused", glance: glance(),
			pause: &boundaries.PauseSummary{Configured: true, Rule: "plan-floor", Stage: "enforce", Sessions: 2, PausePoint: true},
			want:  "paused 2 sessions this week"},
		{name: "no pause point says so", glance: glance(),
			pause: &boundaries.PauseSummary{Configured: true, Rule: "headroom-pause", Stage: "shadow"},
			want:  "sees no tool calls", action: "tokenops hooks install --boundary"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := pauseFindings(Compute(Inputs{Glance: tc.glance, Pause: tc.pause}))
			if tc.want == "" {
				if len(got) != 0 {
					t.Fatalf("findings = %+v, want none", got)
				}
				return
			}
			if len(got) != 1 {
				t.Fatalf("findings = %+v, want one", got)
			}
			if !strings.Contains(got[0].Title, tc.want) {
				t.Errorf("title = %q, want %q in it", got[0].Title, tc.want)
			}
			if !strings.Contains(got[0].Action, tc.action) {
				t.Errorf("action = %q, want %q in it", got[0].Action, tc.action)
			}
		})
	}
}

func TestPresenceFindings(t *testing.T) {
	for _, tc := range []struct {
		name     string
		presence *boundaries.PresenceStatus
		want     []string
	}{
		{name: "unknown says nothing"},
		{name: "not enrolled says nothing", presence: &boundaries.PresenceStatus{State: boundaries.PresenceStateNotEnrolled}},
		{name: "enrolled with nothing to show says nothing", presence: &boundaries.PresenceStatus{State: boundaries.PresenceStateShadow}},
		{name: "approvals that would not have been honoured", presence: &boundaries.PresenceStatus{State: boundaries.PresenceStateShadow, WouldBeIgnored: 2},
			want: []string{"2 approvals would not have been honoured"}},
		{name: "one reads as one", presence: &boundaries.PresenceStatus{State: boundaries.PresenceStateShadow, WouldBeIgnored: 1},
			want: []string{"1 approval would not"}},
		{name: "a replaced key is announced first", presence: &boundaries.PresenceStatus{State: boundaries.PresenceStateHeld, WouldBeIgnored: 1, Note: "approval key replaced at …"},
			want: []string{"The approval key was replaced", "1 approval would not"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := pauseFindings(Compute(Inputs{Presence: tc.presence}))
			if len(got) != len(tc.want) {
				t.Fatalf("findings = %+v, want %d", got, len(tc.want))
			}
			for i, w := range tc.want {
				if !strings.Contains(got[i].Title, w) {
					t.Errorf("finding %d = %q, want %q in it", i, got[i].Title, w)
				}
			}
		})
	}
}
