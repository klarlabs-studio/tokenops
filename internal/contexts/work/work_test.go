package work_test

import (
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
		if work.HasField(forbidden) {
			t.Errorf("work.Work has a %s field; software-specific concepts "+
				"belong in an adapter, not the core ontology", forbidden)
		}
	}
}

// Work decomposes. A goal too large to attempt directly becomes smaller
// goals, and the parent is what lets consumption and outcome roll up.
func TestWorkDecomposes(t *testing.T) {
	parent := work.New("w1", "ship the auth fix", work.Requested(alice, t0))
	child := work.New("w2", "write the failing test", work.Requested(alice, t0)).Under(parent.ID)

	if child.Parent != parent.ID {
		t.Errorf("parent = %q, want %q", child.Parent, parent.ID)
	}
	if parent.Parent != "" {
		t.Error("a root work has a parent")
	}
}

// Constraints are what the requester is not willing to trade away.
// Multi-objective optimization needs them stated, not inferred: cutting
// tokens by half is not a win if it breaks a quality floor nobody wrote
// down.
func TestWorkCarriesConstraints(t *testing.T) {
	w := work.New("w1", "ship it", work.Requested(alice, t0)).
		Constrained(
			work.MaxCost(5.0),
			work.Deadline(t0.Add(2*time.Hour)),
			work.MinQuality(0.8),
		)

	if len(w.Constraints) != 3 {
		t.Fatalf("constraints = %+v", w.Constraints)
	}
	cost, ok := w.Constraint(work.ConstraintMaxCost)
	if !ok || cost.Limit != 5.0 {
		t.Errorf("max cost = %+v, ok = %v", cost, ok)
	}
	if _, ok := w.Constraint(work.ConstraintMaxTokens); ok {
		t.Error("an unstated constraint was reported as present")
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
	if !sub.Delegated() {
		t.Error("a delegated actor does not report itself as such")
	}
	if agent.Delegated() {
		t.Error("a root actor reports itself delegated")
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

// Outcome is what the work produced, as distinct from what it consumed.
// It is the single largest gap between the intent and the code: `grep -r
// Outcome` over internal/ and pkg/ returned no non-test match. Without
// it TokenOps can only optimize consumption — it can make work cheaper
// while making it worse, and have no way to notice.
func TestOutcomeIsSeparateFromConsumption(t *testing.T) {
	o := work.Achieved("e1", t0.Add(time.Minute), work.AssessedByHuman)

	if o.Execution != "e1" {
		t.Errorf("execution = %q", o.Execution)
	}
	if o.Result != work.ResultAchieved {
		t.Errorf("result = %q", o.Result)
	}
	if !o.Known() {
		t.Error("an assessed outcome reports itself unknown")
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
	if o.Known() {
		t.Error("an unassessed outcome reports itself known")
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
	if o.Known() {
		t.Error("the zero Outcome reports itself known")
	}
	if o.Result != work.ResultUnknown {
		t.Errorf("zero result = %q, want unknown", o.Result)
	}
}

// Who assessed an outcome changes how much it is worth. An agent saying
// it finished is weaker evidence than a test suite saying so, which is
// weaker than a human confirming it — and treating them alike is how
// self-reported success becomes measured success.
func TestAssessmentStrengthIsOrdered(t *testing.T) {
	selfReported := work.Achieved("e1", t0, work.AssessedBySelfReport)
	verified := work.Achieved("e1", t0, work.AssessedByVerification)
	human := work.Achieved("e1", t0, work.AssessedByHuman)

	if !verified.StrongerThan(selfReported) {
		t.Error("verification is not stronger than self-report")
	}
	if !human.StrongerThan(verified) {
		t.Error("human confirmation is not stronger than verification")
	}
	if selfReported.StrongerThan(verified) {
		t.Error("self-report outranks verification")
	}
}

// A failed attempt is an outcome too, and a useful one: it is what
// distinguishes work that was abandoned from work that was tried and
// did not succeed.
func TestFailureIsAnOutcome(t *testing.T) {
	o := work.NotAchieved("e1", t0, work.AssessedByVerification, "the test still fails")

	if o.Result != work.ResultNotAchieved {
		t.Errorf("result = %q", o.Result)
	}
	if !o.Known() {
		t.Error("a known failure reports itself unknown")
	}
	if o.Caveat == "" {
		t.Error("the reason was dropped")
	}
}

// Partial is its own answer. Collapsing it into either success or
// failure loses the thing an operator most wants to know.
func TestPartialIsItsOwnResult(t *testing.T) {
	o := work.Partial("e1", t0, work.AssessedByHuman, "two of three checks pass")
	if o.Result != work.ResultPartial {
		t.Errorf("result = %q", o.Result)
	}
	if !o.Known() {
		t.Error("a partial outcome reports itself unknown")
	}
}
