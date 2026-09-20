// Package fromtasks translates the task ledger into the work ontology.
//
// It is an anti-corruption layer, and the corruption it guards against
// is specific: tasks.Task conflates what someone wanted done with the
// attempt at doing it. `{ID, Description, StartedAt, CompletedAt,
// SessionID}` is one record for both, which is why a second attempt at
// the same goal has never been expressible — and comparing attempts is
// what every later phase needs.
//
// The translation is deliberately conservative. The ledger holds less
// than the ontology can express, and inventing the difference would
// defeat the point of introducing the ontology: an outcome nobody
// assessed must stay unknown, not become a success because a marker was
// written.
package fromtasks

import (
	"go.klarlabs.de/tokenops/internal/contexts/tasks"
	"go.klarlabs.de/tokenops/internal/contexts/work"
)

// Reconstructed is one task, split into the concepts it was conflating.
type Reconstructed struct {
	Work      work.Work
	Actor     work.Actor
	Execution work.Execution
	Outcome   work.Outcome
}

// operator is the requester for every task in the ledger.
//
// A task exists because a person asked for it, even when an agent
// carried it out, and the ledger records no finer identity than "whoever
// runs this machine". Attributing the request to the agent instead would
// lose the only human in the chain, which is the relationship the
// ontology exists to keep.
const operator work.ActorID = "operator"

// Convert splits one task into work, an actor, an attempt and an
// outcome.
func Convert(t tasks.Task) Reconstructed {
	actor := actorFor(t)
	w := work.New(workID(t), t.Description,
		work.Requested(work.Actor{ID: operator, Kind: work.ActorHuman}, t.StartedAt))

	e := work.Attempt(executionID(t), w.ID, actor.ID, t.StartedAt)
	if !t.CompletedAt.IsZero() {
		// The attempt ran to completion. That is a claim about the
		// attempt, and a true one; whether the goal was met is a
		// different claim, made below.
		e = e.Ended(t.CompletedAt, work.Succeeded)
	}

	return Reconstructed{Work: w, Actor: actor, Execution: e, Outcome: outcomeFor(t, e)}
}

// ConvertAll translates a ledger, preserving order.
func ConvertAll(ts []tasks.Task) []Reconstructed {
	if len(ts) == 0 {
		return nil
	}
	out := make([]Reconstructed, 0, len(ts))
	for _, t := range ts {
		out = append(out, Convert(t))
	}
	return out
}

// actorFor decides who was working.
//
// A task carrying a session id was worked inside an agent session. One
// without is the operator's own chore, which `task start` explicitly
// supports.
func actorFor(t tasks.Task) work.Actor {
	if t.SessionID != "" {
		return work.Actor{
			ID:         work.ActorID("session:" + t.SessionID),
			Kind:       work.ActorAgent,
			OnBehalfOf: operator,
		}
	}
	return work.Actor{ID: operator, Kind: work.ActorHuman}
}

// outcomeFor judges what the ledger actually supports.
//
// It supports nothing. `task done` is documented as "mark the most
// recent open task as complete" — a boundary the operator draws while
// moving on, not an assessment that the goal was met. Reading it as an
// achieved outcome would manufacture exactly the claim this ontology
// exists to prevent, and would do it across every task ever recorded, so
// the first thing built on Outcome would be built on a fiction.
//
// Recording it as unknown is not a gap to be filled in later by guessing
// harder. It is the correct answer until something actually assesses the
// work, which is what Phase 5 adds.
func outcomeFor(t tasks.Task, e work.Execution) work.Outcome {
	if e.Running() {
		return work.UnknownOutcome(e.ID, "the attempt is still in progress")
	}
	_ = t
	return work.UnknownOutcome(e.ID,
		"the task ledger records a completion marker, which says the attempt ended, "+
			"not that the goal was met; nothing assessed this work")
}

// workID and executionID keep the two apart. They come from one record,
// and a later store keyed on either would merge them if they shared an
// id.
func workID(t tasks.Task) work.ID { return work.ID("work:" + t.ID) }

func executionID(t tasks.Task) work.ID { return work.ID("exec:" + t.ID) }
