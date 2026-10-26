package findings

import (
	"strings"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/boundaries"
	"go.klarlabs.de/tokenops/internal/config"
)

func shadowLoop(sessions int) *boundaries.PauseSummary {
	return &boundaries.PauseSummary{Configured: true, Rule: "loop-breaker", Stage: "shadow", Sessions: sessions, PausePoint: true,
		When: "the agent's calls go round a loop for 4 calls or more with no file changed", Require: "ask"}
}

func TestLoopBreakerFindings(t *testing.T) {
	for _, tc := range []struct {
		name   string
		loop   *boundaries.PauseSummary
		want   string
		action string
	}{
		{name: "unknown says nothing"},
		{name: "not set says nothing", loop: &boundaries.PauseSummary{}},
		{name: "in shadow with nothing to show says nothing", loop: shadowLoop(0)},
		{name: "in shadow shows what it would have paused", loop: shadowLoop(3),
			want: "The loop breaker would have paused 3 sessions this week", action: "tokenops boundaries promote loop-breaker"},
		{name: "enforced shows what it paused",
			loop: &boundaries.PauseSummary{Configured: true, Rule: "loop-breaker", Stage: "enforce", Sessions: 1, PausePoint: true},
			want: "The loop breaker paused 1 session this week"},
		{name: "without the hook says nothing more than the pause does",
			loop: &boundaries.PauseSummary{Configured: true, Rule: "loop-breaker", Stage: "shadow", Sessions: 2}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := pauseFindings(Compute(Inputs{Loop: tc.loop}))
			if tc.want == "" {
				if len(got) != 0 {
					t.Fatalf("findings = %+v, want none", got)
				}
				return
			}
			if len(got) != 1 || !strings.Contains(got[0].Title, tc.want) || !strings.Contains(got[0].Action, tc.action) {
				t.Fatalf("findings = %+v, want one titled %q", got, tc.want)
			}
		})
	}
}

const loopConfig = `boundaries:
  - name: loop-breaker
    action: continue
    when:
      loop_calls: ">= 4"
    require: ask
    stage: shadow
`

// The shadow finding offers the same promote the headroom pause does, in
// the rule's own words, and promoting it makes the rule bind.
func TestActPromotesTheLoopBreaker(t *testing.T) {
	path := writeConfig(t, loopConfig)
	r := Compute(Inputs{Loop: shadowLoop(2)})
	f := byKey(t, r, "boundary:loop-breaker")
	if len(f.Actions) != 1 || f.Actions[0].ID != ActBoundaryPromote ||
		!strings.Contains(f.Actions[0].Confirm, "go round a loop for 4 calls") ||
		!strings.Contains(f.Actions[0].Confirm, "tokenops boundaries demote loop-breaker") {
		t.Fatalf("actions = %+v", f.Actions)
	}
	got, err := Act(r, "boundary:loop-breaker", ActBoundaryPromote, ActEnv{ConfigPath: path, Now: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	if got.Rule != config.LoopBreakerName || got.Stage != "enforce" {
		t.Fatalf("acted = %+v", got)
	}
	cfg, err := config.ReadMutable(path)
	if err != nil || cfg.Boundaries[0].Stage != "enforce" {
		t.Fatalf("stage = %+v, %v", cfg.Boundaries, err)
	}
}
