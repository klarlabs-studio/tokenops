package savings

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	ft "go.klarlabs.de/tokenops/internal/contexts/coaching/followthrough"
	"go.klarlabs.de/tokenops/internal/contexts/learning"
)

func rungs(m map[string]string) func(string) string {
	return func(p string) string { return m[p] }
}

func step(t *testing.T, steps []Step, capability string) Step {
	t.Helper()
	for _, s := range steps {
		if s.Capability == capability {
			return s
		}
	}
	t.Fatalf("no step for %s in %+v", capability, steps)
	return Step{}
}

// The read guard, watching, becomes ready to enforce on its shadow
// evidence, and the step names the command; nothing is changed.
func TestReadGuardGraduatesOnShadowEvidence(t *testing.T) {
	l := &memLedger{}
	for i := range 55 {
		RecordReadGuard(l, t0, fmt.Sprint("r", i), "s", true, 4000)
	}
	r := Build(context.Background(), Deps{Ledger: l}, t0)
	steps := Graduations(r, rungs(map[string]string{"waste": "advise"}), nil)
	s := step(t, steps, learning.CapabilityReadGuard)
	if !s.Ready || s.Next != learning.StageAct || s.Power != "waste" || !strings.Contains(s.Promote, "tokenops coach set waste autonomous") {
		t.Fatalf("read guard = %+v", s)
	}
	if line := Headline(s); !strings.Contains(line, "read guard is ready to enforce") {
		t.Fatalf("headline = %q", line)
	}
}

// Tips graduate from advise only on follow-through and a realized saving.
func TestTipsGraduateOnFollowThroughAndRealized(t *testing.T) {
	l := &memLedger{}
	peaks := map[string]float64{}
	for i := range 6 {
		tr, ho := fmt.Sprint("t", i), fmt.Sprint("h", i)
		RecordTip(l, t0, Tip{ID: tr, Session: tr, Kind: "compact_now", Cohort: learning.CohortTreated, SessionUSD: 10})
		RecordTip(l, t0, Tip{ID: ho, Session: ho, Kind: "compact_now", Cohort: learning.CohortHoldout, SessionUSD: 10})
		peaks[tr], peaks[ho] = 13, 19
	}
	r := Build(context.Background(), Deps{Ledger: l, SessionPeaks: func() map[string]float64 { return peaks }}, t0.Add(48*time.Hour))
	summaries := []ft.Summary{{Power: "context", Channel: ft.ChannelTip, Kind: "compact_now", Followed: 50, Acknowledged: 6, Ignored: 3}}
	s := step(t, Graduations(r, rungs(map[string]string{"context": "advise"}), summaries), learning.CapabilityCompaction)
	if !s.Ready || s.Evidence.Advised != 59 || s.Evidence.Accepted != 56 {
		t.Fatalf("compaction = %+v", s)
	}
	// Too little follow-through: not ready, and the step says which bar.
	summaries[0].Followed = 10
	s = step(t, Graduations(r, rungs(map[string]string{"context": "advise"}), summaries), learning.CapabilityCompaction)
	if s.Ready {
		t.Fatalf("ready on 19 outcomes: %+v", s)
	}
}

// A capability that already acts has no next step and no headline.
func TestActingCapabilityIsNotSuggested(t *testing.T) {
	steps := Graduations(Report{}, rungs(map[string]string{"models": "autonomous"}), nil)
	s := step(t, steps, learning.CapabilityRouting)
	if s.Ready || s.Next != "" || Headline(s) != "" {
		t.Fatalf("routing = %+v", s)
	}
}

// Held tips are evidence only by how their sessions went: one the session
// fixed on its own was not needed, and one a later tip replaced says
// nothing. Before, every held tip not followed counted as right, the
// replaced ones included, so the bar could not fail.
func TestHeldTipsCanFailTheShadowBar(t *testing.T) {
	l := &memLedger{}
	var settled []Resolution
	for i := range 60 {
		id := fmt.Sprint("h", i)
		RecordTip(l, t0, Tip{ID: id, Session: id, Kind: "compact_now", Cohort: Holdout})
		switch {
		case i < 20:
			// The session compacted on its own.
			settled = append(settled, Resolution{ID: id, Signal: learning.SignalCompacted, Followed: true})
		case i < 40:
			settled = append(settled, Resolution{ID: id, Signal: learning.SignalSuperseded})
		default:
			settled = append(settled, Resolution{ID: id, Signal: learning.SignalUnchanged})
		}
	}
	RecordOutcomes(l, t0.Add(time.Hour), settled)
	r := Build(context.Background(), Deps{Ledger: l}, t0.Add(48*time.Hour))
	s := step(t, Graduations(r, rungs(map[string]string{"context": "off"}), nil), learning.CapabilityCompaction)
	if s.Evidence.Shadow != 40 || s.Evidence.ShadowConfirmed != 20 {
		t.Fatalf("shadow %d, confirmed %d; want 40 and 20, the replaced ones left out", s.Evidence.Shadow, s.Evidence.ShadowConfirmed)
	}
	if s.Ready {
		t.Errorf("ready at 50%% shadow precision: %+v", s.Graduation)
	}
}

// The read guard's step carries no precision, and confirms none of its
// calls: each is right by construction.
func TestReadGuardConfirmsNothingByConstruction(t *testing.T) {
	l := &memLedger{}
	for i := range 55 {
		RecordReadGuard(l, t0, fmt.Sprint("r", i), "s", true, 4000)
	}
	s := step(t, Graduations(Build(context.Background(), Deps{Ledger: l}, t0), rungs(map[string]string{"waste": "off"}), nil), learning.CapabilityReadGuard)
	if s.Evidence.ShadowConfirmed != 0 {
		t.Errorf("confirmed %d calls by construction", s.Evidence.ShadowConfirmed)
	}
	for _, c := range s.Criteria {
		if strings.Contains(c.Name, "precision") {
			t.Errorf("graded on %q", c.Name)
		}
	}
}
