package work_test

import (
	"reflect"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/work"
)

var (
	t0    = time.Date(2026, 9, 20, 9, 0, 0, 0, time.UTC)
	alice = work.Actor{ID: "alice", Kind: work.ActorHuman, Name: "Alice"}
	agent = work.Actor{ID: "claude-code", Kind: work.ActorAgent, Name: "Claude Code"}
)

// Work is something someone wants accomplished. The goal is what makes
// it work rather than activity: tasks.Task carries a description and a
// pair of timestamps, which records that something happened without ever
// saying what it was for.
func TestWorkCarriesAGoal(t *testing.T) {
	w := work.New("w1", "ship the auth fix", work.Requested(alice, t0))

	if w.Goal != "ship the auth fix" {
		t.Errorf("goal = %q", w.Goal)
	}
	if w.RequestedBy.ID != alice.ID {
		t.Errorf("requester = %+v", w.RequestedBy)
	}
	if !w.CreatedAt.Equal(t0) {
		t.Errorf("created = %v", w.CreatedAt)
	}
}

// The architectural invariant: no core abstraction may require AI work
// to mean software development. Work is named by a goal and constrained
// by budgets — never by a repository, a commit or a file.
//
// This test is a guard on the type's shape rather than its behaviour,
// which is the point: the drift it prevents happens by someone adding
// one convenient field.
func TestWorkHasNoSoftwareSpecificFields(t *testing.T) {
	for _, forbidden := range []string{
		"Repo", "Repository", "Commit", "Branch", "PullRequest", "PR",
		"File", "Files", "Diff", "Patch", "Build", "Test", "Lint",
	} {
		if _, ok := reflect.TypeFor[work.Work]().FieldByName(forbidden); ok {
			t.Errorf("work.Work has a %s field; software-specific concepts "+
				"belong in an adapter, not the core ontology", forbidden)
		}
	}
}

// An Actor is whoever or whatever performs work. Without it delegation
// cannot be represented at all: the code had a Provider string and a
// SessionID, and neither says who was working.
func TestActorKindsCoverDelegation(t *testing.T) {
	for _, k := range []work.ActorKind{
		work.ActorHuman, work.ActorAgent, work.ActorModel, work.ActorTool, work.ActorSystem,
	} {
		if k == "" {
			t.Error("an actor kind is empty")
		}
	}
	// Delegation is an actor acting on another's behalf, which is the
	// shape multi-agent work takes.
	sub := work.Actor{ID: "subagent-1", Kind: work.ActorAgent, OnBehalfOf: agent.ID}
	if sub.OnBehalfOf != agent.ID {
		t.Errorf("delegation lost: %+v", sub)
	}
}

// An Execution is one attempt at a Work. This is what the codebase had
// no concept of, and it is what makes everything later possible: two
// attempts at the same goal are what an experiment compares.
func TestWorkCanHaveSeveralExecutions(t *testing.T) {
	w := work.New("w1", "ship it", work.Requested(alice, t0))

	first := work.Attempt("e1", w.ID, agent.ID, t0)
	second := work.Attempt("e2", w.ID, agent.ID, t0.Add(time.Hour))

	if first.Work != w.ID || second.Work != w.ID {
		t.Error("executions are not both attempts at the same work")
	}
	if first.ID == second.ID {
		t.Error("two attempts share an id")
	}
	if !first.Running() {
		t.Error("a fresh attempt is not running")
	}
}

// An execution ends in a definite state. "Still running" and "finished
// and we did not record how" must not be the same value.
func TestExecutionEndsInADefiniteState(t *testing.T) {
	e := work.Attempt("e1", "w1", agent.ID, t0).Ended(t0.Add(time.Minute), work.Succeeded)

	if e.Running() {
		t.Error("an ended execution still reports running")
	}
	if e.Status != work.Succeeded {
		t.Errorf("status = %q", e.Status)
	}
	if e.Duration() != time.Minute {
		t.Errorf("duration = %v", e.Duration())
	}
}

// The zero Execution has not started, rather than having succeeded.
func TestZeroExecutionIsNotASuccess(t *testing.T) {
	var e work.Execution
	if e.Status == work.Succeeded {
		t.Error("the zero Execution claims success")
	}
	if e.Complete() {
		t.Error("the zero Execution claims to be complete")
	}
}

// An outcome nobody assessed is unknown, not a success. This is the
// provenance invariant from Phase 1 applied to the categorical case: an
// unassessed attempt reads as a win exactly where it matters most.
func TestUnassessedOutcomeIsUnknownNotSuccess(t *testing.T) {
	o := work.UnknownOutcome("e1", "nothing verified this attempt")

	if o.Result == work.ResultAchieved {
		t.Error("an unassessed outcome claims the work was achieved")
	}
	if o.Assessment != work.AssessedByNothing {
		t.Errorf("assessment = %q", o.Assessment)
	}
	if o.Caveat == "" {
		t.Error("an unknown outcome does not say why")
	}
}

// The zero Outcome is likewise unknown, so an Outcome field that nobody
// filled in does not become a claim of success.
func TestZeroOutcomeIsUnknown(t *testing.T) {
	var o work.Outcome
	if o.Result != work.ResultUnknown {
		t.Errorf("zero result = %q, want unknown", o.Result)
	}
}

// A goal the operator typed and a goal a heuristic guessed are not the
// same claim, and the ontology has to be able to tell them apart. The
// ledger adapter carries what someone wrote; the transcript adapter
// carries what a documented-as-sometimes-wrong boundary detector
// inferred from a first instruction.
//
// Presenting both as "the goal" would put a guess and a statement on
// equal footing, which is the conflation refused everywhere else in
// this codebase.
func TestAnInferredGoalIsDistinguishableFromAStatedOne(t *testing.T) {
	stated := work.New("w1", "ship the auth fix", work.Requested(alice, t0))
	if stated.GoalSource != work.GoalStated {
		t.Errorf("a goal passed to New reports %q; the caller supplied it",
			stated.GoalSource)
	}
	if stated.GoalSource == work.GoalInferred {
		t.Error("a stated goal reports itself inferred")
	}

	guessed := stated.Inferred("split on an idle gap; the title is the first instruction")
	if guessed.GoalSource != work.GoalInferred {
		t.Error("an inferred goal does not report itself inferred")
	}
	if guessed.GoalCaveat == "" {
		t.Error("an inferred goal does not say how it was arrived at")
	}
	// Inferring returns a copy, as every other builder here does.
	if stated.GoalSource == work.GoalInferred {
		t.Error("Inferred mutated the receiver")
	}
}
