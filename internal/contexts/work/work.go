// Package work holds the four primitives TokenOps had no representation
// of: Work, Actor, Execution and Outcome.
//
// They are the difference between a tool that measures consumption and a
// system that can tell whether the consumption was worth it. Measured
// against the code before this package existed:
//
//   - Work was tasks.Task — an operator-marked description and two
//     timestamps. It records that something happened without ever saying
//     what it was for.
//   - Actor did not exist. There was a Provider string and a SessionID,
//     and neither says who was working, so delegation and multi-agent
//     work could not be represented at all.
//   - Execution did not exist. With no concept of an attempt, work cannot
//     have two of them, and nothing can be compared or experimented on.
//   - Outcome did not exist: `grep -r Outcome` over internal/ and pkg/
//     returned no non-test match. Without it TokenOps can only optimize
//     what work consumes — it can make work cheaper while making it
//     worse and have no way to notice.
//
// # The architectural invariant
//
// No abstraction here may require AI work to mean software development.
// Repositories, pull requests, commits, files and coding sessions are
// evidence and domain-specific resources, never universal primitives.
// They belong in adapters layered on top of this, and the drift this
// guards against happens by someone adding one convenient field —
// TestWorkHasNoSoftwareSpecificFields is the tripwire.
//
// # What is deliberately absent
//
// Persistence, event emission and correlation with the existing event
// envelope. Those are a later phase; introducing the meaning first is
// what keeps the storage decisions from defining it.
package work

import (
	"reflect"
	"time"
)

// ID identifies a Work or an Execution. Opaque: the domain never parses
// it, so an adapter is free to make it a ULID, a session id or a hash.
type ID string

// ActorID identifies an Actor.
type ActorID string

// ActorKind classifies what is doing the work. The set is closed so
// surfaces can dispatch on it rather than matching strings.
type ActorKind string

const (
	// ActorHuman — a person.
	ActorHuman ActorKind = "human"
	// ActorAgent — an autonomous program pursuing a goal, which may
	// delegate to other actors.
	ActorAgent ActorKind = "agent"
	// ActorModel — an LLM invoked to produce output. Distinct from an
	// agent: a model answers, an agent decides.
	ActorModel ActorKind = "model"
	// ActorTool — a program an actor invokes.
	ActorTool ActorKind = "tool"
	// ActorSystem — TokenOps itself, or another piece of automation
	// acting without a requester.
	ActorSystem ActorKind = "system"
)

// Actor is whoever or whatever performs or requests work.
type Actor struct {
	ID   ActorID   `json:"id"`
	Kind ActorKind `json:"kind"`
	Name string    `json:"name,omitempty"`
	// OnBehalfOf names the actor that delegated this one. Empty for an
	// actor acting on its own account.
	//
	// This single field is what makes multi-agent work representable: a
	// subagent's consumption and outcome can be attributed to the agent
	// that spawned it, and through it to the human who asked.
	OnBehalfOf ActorID `json:"on_behalf_of,omitempty"`
}

// Delegated reports whether this actor is acting for another.
func (a Actor) Delegated() bool { return a.OnBehalfOf != "" }

// ConstraintKind names what a constraint limits.
type ConstraintKind string

const (
	// ConstraintMaxCost — an upper bound in the currency the spend
	// context reports.
	ConstraintMaxCost ConstraintKind = "max_cost"
	// ConstraintMaxTokens — an upper bound on tokens consumed.
	ConstraintMaxTokens ConstraintKind = "max_tokens"
	// ConstraintDeadline — work not finished by then has failed its
	// requester whatever else it achieved. Carried in Until.
	ConstraintDeadline ConstraintKind = "deadline"
	// ConstraintMinQuality — a floor the outcome must clear, expressed
	// on whatever scale the assessor uses.
	ConstraintMinQuality ConstraintKind = "min_quality"
)

// Constraint is something the requester is not willing to trade away.
//
// Multi-objective optimization needs these stated rather than inferred.
// Halving the tokens a piece of work consumes is not a win if it breaks
// a quality floor nobody wrote down, and an optimizer with no constraints
// to respect will find exactly that trade.
type Constraint struct {
	Kind ConstraintKind `json:"kind"`
	// Limit is the numeric bound, for the kinds that have one.
	Limit float64 `json:"limit,omitempty"`
	// Until is the bound for ConstraintDeadline.
	Until time.Time `json:"until,omitzero"`
}

// MaxCost caps what the work may cost.
func MaxCost(limit float64) Constraint {
	return Constraint{Kind: ConstraintMaxCost, Limit: limit}
}

// MaxTokens caps what the work may consume.
func MaxTokens(limit float64) Constraint {
	return Constraint{Kind: ConstraintMaxTokens, Limit: limit}
}

// Deadline sets when the work stops being useful.
func Deadline(at time.Time) Constraint {
	return Constraint{Kind: ConstraintDeadline, Until: at}
}

// MinQuality sets the floor the outcome must clear.
func MinQuality(limit float64) Constraint {
	return Constraint{Kind: ConstraintMinQuality, Limit: limit}
}

// Requester records who asked for work and when.
type Requester struct {
	Actor
	At time.Time `json:"at"`
}

// Requested builds a Requester.
func Requested(by Actor, at time.Time) Requester {
	return Requester{Actor: by, At: at}
}

// GoalSource says whether a goal was stated or guessed.
//
// A goal the operator typed and a goal a heuristic reconstructed from a
// transcript are not the same claim. The ledger adapter carries what
// someone wrote; the transcript adapter carries what a
// documented-as-sometimes-wrong boundary detector inferred from a first
// instruction. Presenting both as "the goal" would put a guess and a
// statement on equal footing.
type GoalSource string

const (
	// GoalStated — the caller supplied this goal and is asserting it.
	// The zero value, because New takes a goal its caller chose to pass.
	// An adapter that infers one must say so with Inferred.
	GoalStated GoalSource = ""
	// GoalInferred — reconstructed by a heuristic. GoalCaveat says how.
	GoalInferred GoalSource = "inferred"
)

// Work is something someone wants accomplished.
//
// The goal is what makes it work rather than activity. A description and
// a pair of timestamps records that something happened; a goal is what
// lets anything afterwards ask whether it happened well.
type Work struct {
	ID   ID     `json:"id"`
	Goal string `json:"goal"`
	// GoalSource says whether the goal was stated or reconstructed.
	GoalSource GoalSource `json:"goal_source,omitempty"`
	// GoalCaveat explains how an inferred goal was arrived at — a
	// boundary you can see is one you can argue with.
	GoalCaveat string `json:"goal_caveat,omitempty"`
	// Parent is the work this one decomposes from. Empty at the root.
	// Decomposition is what lets consumption and outcome roll up from
	// the attempts that actually burned tokens to the goal a person
	// recognises.
	Parent ID `json:"parent,omitempty"`
	// RequestedBy is who wants this accomplished.
	RequestedBy Requester `json:"requested_by"`
	// Constraints are the bounds the requester set.
	Constraints []Constraint `json:"constraints,omitempty"`
	// CreatedAt is when the work was recorded.
	CreatedAt time.Time `json:"created_at"`
}

// New records a piece of work.
func New(id ID, goal string, by Requester) Work {
	return Work{ID: id, Goal: goal, RequestedBy: by, CreatedAt: by.At}
}

// Inferred marks the goal as reconstructed rather than stated, and says
// how. Returns a copy.
func (w Work) Inferred(how string) Work {
	w.GoalSource = GoalInferred
	w.GoalCaveat = how
	return w
}

// GoalInferred reports whether the goal was guessed rather than given.
func (w Work) GoalInferred() bool { return w.GoalSource == GoalInferred }

// Under makes this work a decomposition of parent. Returns a copy.
func (w Work) Under(parent ID) Work {
	w.Parent = parent
	return w
}

// Constrained attaches bounds. Returns a copy.
func (w Work) Constrained(cs ...Constraint) Work {
	w.Constraints = append(append([]Constraint(nil), w.Constraints...), cs...)
	return w
}

// Constraint returns the bound of the given kind, if the requester set
// one. The bool is what keeps an unstated constraint from reading as a
// limit of zero.
func (w Work) Constraint(kind ConstraintKind) (Constraint, bool) {
	for _, c := range w.Constraints {
		if c.Kind == kind {
			return c, true
		}
	}
	return Constraint{}, false
}

// HasField reports whether Work carries a field of the given name.
//
// It exists for the invariant test. The rule it guards — that no core
// abstraction may assume AI work means software development — is
// violated one convenient field at a time, and a test that names the
// fields it refuses is the only form of that rule a compiler can check.
func HasField(name string) bool {
	_, ok := reflect.TypeFor[Work]().FieldByName(name)
	return ok
}

// Status is where an attempt ended up.
type Status string

const (
	// StatusRunning — the attempt is in progress. The zero Status.
	StatusRunning Status = ""
	// Succeeded — the attempt ran to completion. Note that this is about
	// the attempt, not the goal: an execution can succeed while its
	// Outcome says the work was not achieved.
	Succeeded Status = "succeeded"
	// Failed — the attempt could not complete.
	Failed Status = "failed"
	// Abandoned — the attempt was stopped before it could resolve.
	Abandoned Status = "abandoned"
)

// Execution is one attempt at a Work.
//
// This is the concept whose absence made everything downstream
// impossible. Work with no notion of an attempt cannot have two of them,
// so nothing can be compared, no baseline exists, and an experiment has
// nothing to run against. Every later phase stands on this type.
type Execution struct {
	ID ID `json:"id"`
	// Work is what this attempt is an attempt at.
	Work ID `json:"work"`
	// By is the actor performing it.
	By ActorID `json:"by"`

	StartedAt time.Time `json:"started_at"`
	EndedAt   time.Time `json:"ended_at,omitzero"`
	Status    Status    `json:"status,omitempty"`
}

// Attempt starts an execution of work by an actor.
func Attempt(id, of ID, by ActorID, at time.Time) Execution {
	return Execution{ID: id, Work: of, By: by, StartedAt: at}
}

// Ended closes an attempt in a definite state. Returns a copy.
func (e Execution) Ended(at time.Time, status Status) Execution {
	e.EndedAt = at
	e.Status = status
	return e
}

// Running reports whether the attempt is still in progress.
func (e Execution) Running() bool { return e.EndedAt.IsZero() }

// Complete reports whether the attempt finished and said how. "Still
// running" and "finished without recording how" must not be the same
// answer, which is why this asks for both.
func (e Execution) Complete() bool {
	return !e.EndedAt.IsZero() && e.Status != StatusRunning
}

// Duration is how long the attempt took. Zero while still running —
// callers that want elapsed time on a live attempt should subtract
// StartedAt from their own clock rather than have this invent one.
func (e Execution) Duration() time.Duration {
	if e.Running() {
		return 0
	}
	return e.EndedAt.Sub(e.StartedAt)
}

// Result says whether the work was accomplished.
type Result string

const (
	// ResultUnknown — nobody assessed it. The zero Result, deliberately:
	// an Outcome field nobody filled in must not read as a success.
	ResultUnknown Result = ""
	// ResultAchieved — the goal was met.
	ResultAchieved Result = "achieved"
	// ResultPartial — some of it was. Its own answer, because collapsing
	// it into either success or failure loses what an operator most
	// wants to know.
	ResultPartial Result = "partial"
	// ResultNotAchieved — it was attempted and the goal was not met.
	ResultNotAchieved Result = "not_achieved"
)

// Assessment says who or what judged an outcome. It is provenance, in
// the sense Phase 1 gave the word, applied to the categorical case.
type Assessment string

const (
	// AssessedByNothing — no assessment was made. The zero value.
	AssessedByNothing Assessment = ""
	// AssessedBySelfReport — the actor doing the work said so. The
	// weakest evidence there is, and the easiest to collect.
	AssessedBySelfReport Assessment = "self_report"
	// AssessedByVerification — something independent checked: a test
	// suite, a build, a validator.
	AssessedByVerification Assessment = "verification"
	// AssessedByHuman — a person confirmed it.
	AssessedByHuman Assessment = "human"
)

// strength orders assessments. Treating an agent's own report as equal
// to a passing test is how self-reported success quietly becomes
// measured success.
func (a Assessment) strength() int {
	switch a {
	case AssessedBySelfReport:
		return 1
	case AssessedByVerification:
		return 2
	case AssessedByHuman:
		return 3
	default:
		return 0
	}
}

// Outcome is what an execution produced, as distinct from what it
// consumed.
//
// Consumption was always measurable and always measured. Outcome was
// absent entirely, which is why TokenOps could report that an
// optimization saved tokens and had no way to notice it had made the
// work worse.
type Outcome struct {
	// Execution is the attempt this judges.
	Execution ID `json:"execution"`
	// Result is whether the goal was met.
	Result Result `json:"result,omitempty"`
	// Assessment is who says so. An outcome is worth what its assessor
	// is worth.
	Assessment Assessment `json:"assessment,omitempty"`
	// AssessedAt is when the judgement was made, which may be long after
	// the execution ended.
	AssessedAt time.Time `json:"assessed_at,omitzero"`
	// Caveat explains a partial or failed result, or says why an unknown
	// is unknown.
	Caveat string `json:"caveat,omitempty"`
}

// Achieved records that an execution met its goal.
func Achieved(of ID, at time.Time, by Assessment) Outcome {
	return Outcome{Execution: of, Result: ResultAchieved, Assessment: by, AssessedAt: at}
}

// Partial records that an execution met part of its goal.
func Partial(of ID, at time.Time, by Assessment, why string) Outcome {
	return Outcome{Execution: of, Result: ResultPartial, Assessment: by, AssessedAt: at, Caveat: why}
}

// NotAchieved records that an execution was tried and did not meet its
// goal. A recorded failure is useful: it is what distinguishes work that
// was abandoned from work that was attempted and did not succeed.
func NotAchieved(of ID, at time.Time, by Assessment, why string) Outcome {
	return Outcome{Execution: of, Result: ResultNotAchieved, Assessment: by, AssessedAt: at, Caveat: why}
}

// UnknownOutcome records that nothing assessed an execution, and why.
//
// This is the honest default and must stay easy to reach. Most work
// TokenOps observes is never assessed at all, and the alternative to
// recording that plainly is letting an unassessed attempt read as a win.
func UnknownOutcome(of ID, why string) Outcome {
	return Outcome{Execution: of, Result: ResultUnknown, Caveat: why}
}

// Known reports whether anything actually judged this outcome.
func (o Outcome) Known() bool {
	return o.Result != ResultUnknown && o.Assessment != AssessedByNothing
}

// StrongerThan reports whether this outcome rests on better evidence
// than another.
func (o Outcome) StrongerThan(other Outcome) bool {
	return o.Assessment.strength() > other.Assessment.strength()
}
