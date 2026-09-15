package story

import (
	"testing"

	"go.klarlabs.de/tokenops/internal/contexts/governance/agentdx"
)

func TestIsContinuationRecognisesAnswers(t *testing.T) {
	for _, p := range []string{
		"Go", "go", "  proceed  ", "Proceed.", "continue", "yes", "OK",
		"do it", "ship it", "split", "fix them", "looks good", "no",
		"and also add a test", "now run the build", "actually, revert that",
	} {
		if !IsContinuation(agentdx.Unit{Prompt: p}) {
			t.Errorf("IsContinuation(%q) = false, want true", p)
		}
	}
}

// Brevity is not continuation. An earlier version treated any instruction
// of three words or fewer as one, which swallowed exactly the short
// instructions that carry real work.
func TestIsContinuationDoesNotSwallowShortRealWork(t *testing.T) {
	for _, p := range []string{
		"fix the parser", "add tests", "run the build", "unrelated thing",
		"go through the parser", "continue the migration to v2",
	} {
		if IsContinuation(agentdx.Unit{Prompt: p}) {
			t.Errorf("IsContinuation(%q) = true, want false — it carries work of its own", p)
		}
	}
}

// A rejection is a statement about work already in flight, so it can
// never open a task. This is the strongest signal available because it
// comes from the transcript rather than from wording.
func TestRejectionIsAlwaysContinuation(t *testing.T) {
	u := agentdx.Unit{Prompt: "that is not what I meant at all, try the other approach", PromptRejects: true}
	if !IsContinuation(u) {
		t.Error("a rejecting instruction opened a task; it refers to work already underway")
	}
}

// Without prompt text there is nothing to judge, and boundaries must fall
// exactly where they did before text was carried at all.
func TestNoTextIsNotAContinuation(t *testing.T) {
	if IsContinuation(agentdx.Unit{}) {
		t.Error("an empty prompt was treated as a continuation; a textless scan must group as it always did")
	}
}

func TestContinuationNeverSplitsATask(t *testing.T) {
	// A long pause followed by "Go" is the operator answering a question,
	// not starting new work.
	units := []agentdx.Unit{
		unit("s1", "rewrite the boundary heuristic", 0),
		unit("s1", "Go", 120),
	}
	tasks := Group(units, Options{})
	if len(tasks) != 1 {
		t.Fatalf("got %d tasks, want 1 — a 2-hour pause then \"Go\" is still the same work", len(tasks))
	}
	if tasks[0].Title != "rewrite the boundary heuristic" {
		t.Errorf("title = %q, want the substantive instruction", tasks[0].Title)
	}
}

// A task can open with an answer, with the work named one instruction
// later. Titling from the opener produced "Go" or "Proceed" for 71% of
// tasks on real history.
func TestTitleSkipsLeadingContinuations(t *testing.T) {
	units := []agentdx.Unit{
		unit("s1", "first piece of work", 0),
		unit("s1", "Proceed", 200), // new task: gap, but a continuation…
		unit("s1", "now the second thing", 400),
	}
	tasks := Group(units, Options{})
	for _, tk := range tasks {
		if tk.Title == "Proceed" {
			t.Fatalf("a task was titled %q; titles come from substantive instructions", tk.Title)
		}
	}
}
