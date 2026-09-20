package reconstruct_test

import (
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/reconstruct"
	"go.klarlabs.de/tokenops/internal/contexts/governance/agentdx"
	"go.klarlabs.de/tokenops/internal/contexts/work"
)

var t0 = time.Date(2026, 9, 20, 9, 0, 0, 0, time.UTC)

func units() []agentdx.Unit {
	return []agentdx.Unit{
		{SessionID: "sess-1", Prompt: "ship the auth fix", Start: t0, End: t0.Add(time.Minute), Turns: 2},
		{SessionID: "sess-1", Prompt: "rewrite the changelog for the release", Start: t0.Add(2 * time.Hour),
			End: t0.Add(2*time.Hour + time.Minute), Turns: 1},
	}
}

// Grouping transcript units into stories and translating those into the
// ontology is one capability, not two things each surface assembles.
// The CLI and the MCP story tool both want it, and the capability
// ratchet exists precisely to stop the second one reimplementing the
// first.
func TestUnitsBecomeReconstructedWork(t *testing.T) {
	got := reconstruct.FromUnits(units(), reconstruct.Options{})

	if len(got) != 2 {
		t.Fatalf("a two-hour gap should split the work; got %d", len(got))
	}
	if got[0].Work.ID == got[1].Work.ID {
		t.Error("two stories share a work id")
	}
	for _, r := range got {
		if r.Execution.Work != r.Work.ID {
			t.Errorf("execution does not point at its work: %+v", r)
		}
		// Everything reconstructed from a transcript is a guess about
		// the goal and silent about the outcome.
		if r.Work.GoalSource != work.GoalInferred {
			t.Errorf("goal source = %q, want inferred", r.Work.GoalSource)
		}
		if r.Outcome.Known() {
			t.Error("a reconstructed story produced a known outcome")
		}
	}
}

// The idle gap is the knob that decides what counts as one piece of
// work, and both surfaces expose it. Passing it through the capability
// keeps them from disagreeing about the default.
func TestTheIdleGapIsHonoured(t *testing.T) {
	// A negative gap disables idle splitting, so a session is one story.
	got := reconstruct.FromUnits(units(), reconstruct.Options{IdleGap: -1})
	if len(got) != 1 {
		t.Fatalf("splitting was not disabled; got %d stories", len(got))
	}
}

// Newest first: the work you are most likely asking about is the work
// you just did. The CLI did this itself; doing it here means the MCP
// surface cannot forget to.
func TestNewestWorkComesFirst(t *testing.T) {
	got := reconstruct.FromUnits(units(), reconstruct.Options{})
	if len(got) < 2 {
		t.Fatalf("want 2, got %d", len(got))
	}
	if !got[0].Execution.StartedAt.After(got[1].Execution.StartedAt) {
		t.Errorf("order is oldest-first: %v then %v",
			got[0].Execution.StartedAt, got[1].Execution.StartedAt)
	}
}

// A limit of zero means everything, matching the flag's documented
// behaviour rather than returning nothing.
func TestZeroLimitReturnsEverything(t *testing.T) {
	if got := reconstruct.FromUnits(units(), reconstruct.Options{Limit: 0}); len(got) != 2 {
		t.Errorf("a zero limit returned %d", len(got))
	}
}

func TestLimitTrimsToTheNewest(t *testing.T) {
	got := reconstruct.FromUnits(units(), reconstruct.Options{Limit: 1})
	if len(got) != 1 {
		t.Fatalf("want 1, got %d", len(got))
	}
	if !got[0].Execution.StartedAt.Equal(t0.Add(2 * time.Hour)) {
		t.Errorf("the limit kept the older story: %v", got[0].Execution.StartedAt)
	}
}

func TestNoUnitsReconstructNothing(t *testing.T) {
	if got := reconstruct.FromUnits(nil, reconstruct.Options{}); len(got) != 0 {
		t.Errorf("got %+v", got)
	}
}
