package fromtasks_test

import (
	"strings"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/tasks"
	"go.klarlabs.de/tokenops/internal/contexts/work"
	"go.klarlabs.de/tokenops/internal/contexts/work/fromtasks"
)

var t0 = time.Date(2026, 9, 20, 9, 0, 0, 0, time.UTC)

// tasks.Task conflates two things the new model keeps apart: what
// someone wanted done, and the attempt at doing it. One task therefore
// becomes one Work and one Execution — which is what makes a second
// attempt at the same goal expressible at all, and it never was before.
func TestATaskBecomesWorkAndOneExecution(t *testing.T) {
	got := fromtasks.Convert(tasks.Task{
		ID:          "t1",
		Description: "ship the auth fix",
		StartedAt:   t0,
		CompletedAt: t0.Add(30 * time.Minute),
		SessionID:   "sess-1",
	})

	if got.Work.Goal != "ship the auth fix" {
		t.Errorf("goal = %q", got.Work.Goal)
	}
	if got.Execution.Work != got.Work.ID {
		t.Errorf("the execution is not an attempt at the work: %+v", got)
	}
	if got.Execution.Running() {
		t.Error("a completed task produced a running execution")
	}
	if got.Execution.Duration() != 30*time.Minute {
		t.Errorf("duration = %v", got.Execution.Duration())
	}
}

// A completion marker says the attempt ended. It does not say the goal
// was met — `task done` is documented as "mark the most recent open task
// as complete", a boundary the operator draws while moving on.
//
// Translating it into an achieved outcome would manufacture the exact
// claim this package exists to stop: an unassessed attempt reading as a
// win. The outcome is unknown, and it says why.
func TestACompletedTaskDoesNotClaimTheGoalWasMet(t *testing.T) {
	got := fromtasks.Convert(tasks.Task{
		ID: "t1", Description: "ship it",
		StartedAt: t0, CompletedAt: t0.Add(time.Minute),
	})

	if got.Outcome.Result == work.ResultAchieved {
		t.Error("a completion marker was read as the goal being achieved")
	}
	if got.Outcome.Known() {
		t.Error("an unassessed task produced a known outcome")
	}
	if !strings.Contains(got.Outcome.Caveat, "marker") {
		t.Errorf("the caveat does not explain the limit of the evidence: %q", got.Outcome.Caveat)
	}
	// The attempt itself did complete, which is a different claim and a
	// true one.
	if got.Execution.Status != work.Succeeded {
		t.Errorf("execution status = %q, want succeeded", got.Execution.Status)
	}
}

// An open task is a running attempt, not a failed one.
func TestAnOpenTaskIsARunningExecution(t *testing.T) {
	got := fromtasks.Convert(tasks.Task{ID: "t1", Description: "in progress", StartedAt: t0})

	if !got.Execution.Running() {
		t.Error("an open task produced a finished execution")
	}
	if got.Execution.Status == work.Failed {
		t.Error("an open task was read as failed")
	}
	if got.Outcome.Known() {
		t.Error("work still in progress produced a known outcome")
	}
}

// A task started inside a session was worked by an agent. One started
// without a session is an operator's own chore, which `task start` is
// explicitly documented to allow.
func TestTheActorReflectsWhoWasWorking(t *testing.T) {
	inSession := fromtasks.Convert(tasks.Task{ID: "t1", StartedAt: t0, SessionID: "sess-1"})
	if inSession.Actor.Kind != work.ActorAgent {
		t.Errorf("a session task was attributed to %q, want agent", inSession.Actor.Kind)
	}
	if inSession.Execution.By != inSession.Actor.ID {
		t.Errorf("the execution names a different actor: %+v", inSession)
	}

	chore := fromtasks.Convert(tasks.Task{ID: "t2", StartedAt: t0})
	if chore.Actor.Kind != work.ActorHuman {
		t.Errorf("a session-less task was attributed to %q, want human", chore.Actor.Kind)
	}
}

// The requester is the operator either way: a task exists because a
// person asked for it, even when an agent carried it out. Attributing
// the request to the agent would lose the only human in the chain.
func TestTheRequesterIsAlwaysTheOperator(t *testing.T) {
	got := fromtasks.Convert(tasks.Task{ID: "t1", StartedAt: t0, SessionID: "sess-1"})
	if got.Work.RequestedBy.Kind != work.ActorHuman {
		t.Errorf("requester kind = %q, want human", got.Work.RequestedBy.Kind)
	}
}

// Work and Execution ids must not collide: they are different things
// and a later store keyed on either would merge them.
func TestWorkAndExecutionHaveDistinctIDs(t *testing.T) {
	got := fromtasks.Convert(tasks.Task{ID: "t1", StartedAt: t0})
	if string(got.Work.ID) == string(got.Execution.ID) {
		t.Errorf("work and execution share the id %q", got.Work.ID)
	}
	if got.Work.ID == "" || got.Execution.ID == "" {
		t.Errorf("an id is empty: %+v", got)
	}
}

// Converting the whole ledger preserves order and count, so a caller can
// reason about a session's work without re-reading the file.
func TestConvertAllPreservesTheLedger(t *testing.T) {
	in := []tasks.Task{
		{ID: "t1", Description: "first", StartedAt: t0},
		{ID: "t2", Description: "second", StartedAt: t0.Add(time.Hour)},
	}
	got := fromtasks.ConvertAll(in)
	if len(got) != 2 {
		t.Fatalf("want 2, got %d", len(got))
	}
	if got[0].Work.Goal != "first" || got[1].Work.Goal != "second" {
		t.Errorf("order was not preserved: %+v", got)
	}
}

// An empty ledger converts to nothing, not to one empty item.
func TestConvertAllOfNothingIsNothing(t *testing.T) {
	if got := fromtasks.ConvertAll(nil); len(got) != 0 {
		t.Errorf("got %+v", got)
	}
}

// A task with no description still converts: the goal is missing, which
// is a fact worth carrying rather than a reason to drop the record.
func TestATaskWithNoDescriptionStillConverts(t *testing.T) {
	got := fromtasks.Convert(tasks.Task{ID: "t1", StartedAt: t0})
	if got.Work.ID == "" {
		t.Error("a task with no description was dropped")
	}
	if got.Work.Goal != "" {
		t.Errorf("a goal was invented: %q", got.Work.Goal)
	}
}
