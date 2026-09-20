package fromstory_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/governance/agentdx"
	"go.klarlabs.de/tokenops/internal/contexts/governance/story"
	"go.klarlabs.de/tokenops/internal/contexts/work"
	"go.klarlabs.de/tokenops/internal/contexts/work/fromstory"
)

var t0 = time.Date(2026, 9, 20, 9, 0, 0, 0, time.UTC)

func task(title, session string, start, end time.Time, b story.Boundary) story.Task {
	return story.Task{
		Title: title, SessionID: session, Provider: "anthropic",
		Start: start, End: end, Boundary: b,
		Units: []agentdx.Unit{{SessionID: session, Start: start, End: end, Turns: 3}},
	}
}

// fromtasks reconstructs work from a ledger the operator marked by hand,
// which most people never run. This reconstructs it from transcripts the
// agent produced anyway — which is what makes Understand a stage that
// happens rather than one that could.
func TestAStoryTaskBecomesWorkAndAnExecution(t *testing.T) {
	got := fromstory.Convert(task("ship the auth fix", "sess-1",
		t0, t0.Add(30*time.Minute), story.BoundarySessionStart))

	if got.Work.Goal != "ship the auth fix" {
		t.Errorf("goal = %q", got.Work.Goal)
	}
	if got.Execution.Work != got.Work.ID {
		t.Errorf("the execution is not an attempt at the work: %+v", got)
	}
	if got.Execution.Duration() != 30*time.Minute {
		t.Errorf("duration = %v", got.Execution.Duration())
	}
}

// The distinction that matters between this adapter and the ledger one.
// A story title is inferred from the first instruction by a heuristic
// that is documented as sometimes wrong. Presenting it as a goal the
// operator stated would put a guess and a statement on equal footing —
// the same conflation the measurement and outcome work refuses
// elsewhere.
func TestAnInferredGoalSaysItWasInferred(t *testing.T) {
	got := fromstory.Convert(task("fix the flaky test", "sess-1",
		t0, t0.Add(time.Minute), story.BoundaryIdle))

	if got.Work.GoalSource != work.GoalInferred {
		t.Errorf("goal source = %q, want inferred", got.Work.GoalSource)
	}
	// The reason has to say what split the work, because a boundary you
	// can see is one you can argue with.
	if !strings.Contains(got.Work.GoalCaveat, string(story.BoundaryIdle)) {
		t.Errorf("the caveat does not name the boundary: %q", got.Work.GoalCaveat)
	}
}

// The session is the actor, in the same form the ledger adapter and the
// proxy produce, so all three name one actor without a translation
// table between them.
func TestTheSessionIsTheActor(t *testing.T) {
	got := fromstory.Convert(task("anything", "sess-1", t0, t0, story.BoundarySessionStart))

	if got.Actor.ID != "session:sess-1" {
		t.Errorf("actor = %q, want session:sess-1", got.Actor.ID)
	}
	if got.Actor.Kind != work.ActorAgent {
		t.Errorf("actor kind = %q, want agent", got.Actor.Kind)
	}
	if got.Execution.By != got.Actor.ID {
		t.Errorf("the execution names a different actor: %+v", got)
	}
}

// Nothing assessed this work either. A transcript records what was
// said, not whether the goal was met, and an agent's closing "done!" is
// self-report at best — which is not evidence the ontology will accept
// as an outcome.
func TestAReconstructedStoryHasNoKnownOutcome(t *testing.T) {
	got := fromstory.Convert(task("ship it", "sess-1", t0, t0.Add(time.Minute), story.BoundaryIdle))

	if got.Outcome.Known() {
		t.Error("a reconstructed story produced a known outcome")
	}
	if got.Outcome.Caveat == "" {
		t.Error("the unknown outcome does not say why")
	}
}

// An unfinished story is a running attempt. A transcript whose last turn
// has no end is work still in progress, not work that failed.
func TestAnUnfinishedStoryIsRunning(t *testing.T) {
	got := fromstory.Convert(task("in progress", "sess-1", t0, time.Time{}, story.BoundarySessionStart))

	if !got.Execution.Running() {
		t.Error("an unfinished story produced a finished execution")
	}
	if got.Execution.Status == work.Failed {
		t.Error("an unfinished story was read as failed")
	}
}

// Two stories in one session are two pieces of work, not two attempts at
// one. They have different goals; only the actor is shared.
func TestTwoStoriesInASessionAreDistinctWork(t *testing.T) {
	got := fromstory.ConvertAll([]story.Task{
		task("first goal", "sess-1", t0, t0.Add(time.Minute), story.BoundarySessionStart),
		task("second goal", "sess-1", t0.Add(time.Hour), t0.Add(time.Hour+time.Minute), story.BoundaryIdle),
	})

	if len(got) != 2 {
		t.Fatalf("want 2, got %d", len(got))
	}
	if got[0].Work.ID == got[1].Work.ID {
		t.Error("two stories share one work id")
	}
	if got[0].Actor.ID != got[1].Actor.ID {
		t.Error("two stories in one session named different actors")
	}
}

// Ids are derived from the story's own identity so re-running the
// reconstruction over the same transcripts produces the same ids.
// Without that, every scan would invent new work and nothing could be
// compared across runs — which is the whole point of having executions.
func TestReconstructionIsStableAcrossRuns(t *testing.T) {
	in := task("ship it", "sess-1", t0, t0.Add(time.Minute), story.BoundaryIdle)

	first := fromstory.Convert(in)
	second := fromstory.Convert(in)

	if first.Work.ID != second.Work.ID {
		t.Errorf("work id changed between runs: %q then %q", first.Work.ID, second.Work.ID)
	}
	if first.Execution.ID != second.Execution.ID {
		t.Errorf("execution id changed between runs: %q then %q",
			first.Execution.ID, second.Execution.ID)
	}
}

// Work and execution ids stay distinct, as in the ledger adapter: a
// store keyed on either would merge them otherwise.
func TestWorkAndExecutionIDsDiffer(t *testing.T) {
	got := fromstory.Convert(task("ship it", "sess-1", t0, t0, story.BoundaryIdle))
	if string(got.Work.ID) == string(got.Execution.ID) {
		t.Errorf("work and execution share the id %q", got.Work.ID)
	}
}

// A story with no title still converts. An untitled piece of work is a
// fact worth carrying, not a reason to drop the record — and inventing
// a title would be the fiction this whole ontology avoids.
func TestAnUntitledStoryStillConverts(t *testing.T) {
	got := fromstory.Convert(task("", "sess-1", t0, t0, story.BoundarySessionStart))
	if got.Work.ID == "" {
		t.Error("an untitled story was dropped")
	}
	if got.Work.Goal != "" {
		t.Errorf("a goal was invented: %q", got.Work.Goal)
	}
}

func TestConvertAllOfNothingIsNothing(t *testing.T) {
	if got := fromstory.ConvertAll(nil); len(got) != 0 {
		t.Errorf("got %+v", got)
	}
}

// Field names without JSON tags are Go identifiers, so renaming a field
// silently breaks every consumer of the serialised form. That defect
// already exists elsewhere in this codebase (workflow.Trace serialises
// as TotalTotalTokens and StepCount); repeating it in a type built for
// an agent to read would be careless.
func TestReconstructedSerialisesWithStableNames(t *testing.T) {
	body, err := json.Marshal(fromstory.Convert(
		task("ship it", "sess-1", t0, t0.Add(time.Minute), story.BoundaryIdle)))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, want := range []string{`"work"`, `"actor"`, `"execution"`, `"outcome"`} {
		if !strings.Contains(string(body), want) {
			t.Errorf("missing %s in:\n%s", want, body)
		}
	}
	for _, unwanted := range []string{`"Work"`, `"Actor"`, `"Execution"`, `"Outcome"`} {
		if strings.Contains(string(body), unwanted) {
			t.Errorf("a Go identifier leaked into the wire form: %s in\n%s", unwanted, body)
		}
	}
}
