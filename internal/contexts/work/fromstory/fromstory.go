// Package fromstory reconstructs work from agent transcripts.
//
// It is the second adapter into the work ontology and the one that makes
// Understand a stage that happens rather than one that could. fromtasks
// reads a ledger the operator marked by hand, which most people never
// run; this reads the transcripts the agent produced anyway.
//
// The difference from the ledger adapter is epistemic, and the package
// exists to keep it visible. A story's title is inferred by a boundary
// heuristic that is documented as sometimes wrong, so the goal it
// produces is marked inferred and carries the boundary that split it —
// a boundary you can see is one you can argue with. The ledger's goals
// were typed by a person and are not marked.
package fromstory

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"go.klarlabs.de/tokenops/internal/contexts/governance/story"
	"go.klarlabs.de/tokenops/internal/contexts/work"
)

// Reconstructed is one story, as the ontology sees it.
//
// The JSON tags are deliberate. Without them the wire names are Go
// identifiers, so renaming a field silently breaks every consumer —
// which is a defect this codebase already carries elsewhere
// (workflow.Trace serialises as TotalTotalTokens and StepCount) and one
// worth not repeating in a type built for an agent to read.
type Reconstructed struct {
	Work      work.Work      `json:"work"`
	Actor     work.Actor     `json:"actor"`
	Execution work.Execution `json:"execution"`
	Outcome   work.Outcome   `json:"outcome"`
}

// operator is who the work was ultimately for.
//
// A transcript records an agent working, but the agent is working
// because a person asked. Attributing the request to the agent would
// lose the only human in the chain, which is the relationship the
// ontology exists to keep — the same choice the ledger adapter makes.
const operator work.ActorID = "operator"

// Convert turns one reconstructed story into work, an actor, an attempt
// and an outcome.
func Convert(t story.Task) Reconstructed {
	actor := work.Actor{
		ID:         work.ActorID("session:" + t.SessionID),
		Kind:       work.ActorAgent,
		OnBehalfOf: operator,
	}

	w := work.New(workID(t), t.Title,
		work.Requested(work.Actor{ID: operator, Kind: work.ActorHuman}, t.Start)).
		Inferred(inferenceNote(t))

	e := work.Attempt(executionID(t), w.ID, actor.ID, t.Start)
	if !t.End.IsZero() {
		// The transcript ran out of turns for this story. That is a
		// claim about the attempt ending, not about the goal being met.
		e = e.Ended(t.End, work.Succeeded)
	}

	return Reconstructed{Work: w, Actor: actor, Execution: e, Outcome: outcomeFor(e)}
}

// ConvertAll reconstructs a batch, preserving order.
func ConvertAll(ts []story.Task) []Reconstructed {
	if len(ts) == 0 {
		return nil
	}
	out := make([]Reconstructed, 0, len(ts))
	for _, t := range ts {
		out = append(out, Convert(t))
	}
	return out
}

// inferenceNote says how the goal was arrived at.
func inferenceNote(t story.Task) string {
	return fmt.Sprintf(
		"reconstructed from the transcript: split on %s, and the title is the "+
			"first instruction of the group — a heuristic that is sometimes wrong",
		t.Boundary)
}

// outcomeFor judges what a transcript supports, which is nothing.
//
// A transcript records what was said, not whether the goal was met. An
// agent's closing "done!" is self-report at best, and self-report is the
// weakest evidence the ontology recognises — recording it as an achieved
// outcome would make every reconstructed story a success, which is
// precisely the fiction Outcome was introduced to prevent.
func outcomeFor(e work.Execution) work.Outcome {
	if e.Running() {
		return work.UnknownOutcome(e.ID, "the transcript has no closing turn for this story yet")
	}
	return work.UnknownOutcome(e.ID,
		"a transcript records what was said, not whether the goal was met; "+
			"nothing assessed this work")
}

// storyKey identifies a story by what it is rather than by when it was
// read.
//
// Stability is the point. Re-running the reconstruction over the same
// transcripts has to produce the same ids, or every scan invents new
// work, nothing can be compared across runs, and the executions become
// useless for exactly the comparison they exist to enable.
func storyKey(t story.Task) string {
	sum := sha256.Sum256(fmt.Appendf(nil, "%s\x00%s\x00%d",
		t.SessionID, t.Title, t.Start.UTC().UnixNano()))
	return hex.EncodeToString(sum[:8])
}

func workID(t story.Task) work.ID { return work.ID("work:story:" + storyKey(t)) }

func executionID(t story.Task) work.ID { return work.ID("exec:story:" + storyKey(t)) }
