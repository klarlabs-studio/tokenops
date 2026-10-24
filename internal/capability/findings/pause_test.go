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
