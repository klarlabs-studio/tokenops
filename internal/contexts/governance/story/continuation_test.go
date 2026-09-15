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

// The lexicon is extended from data, not from imagination. Every string
// below titled a real task in 30 days of history and every one of them
// continued the instruction before it.
func TestIsContinuationRecognisesTheObservedMisses(t *testing.T) {
	for _, group := range []struct {
		why    string
		inputs []string
	}{
		{"progress questions", []string{
			"Status", "status", "What's next", "What’s next", "Anything else?",
			"Any blocker?", "Any gaps", "Any new finding?", "What do you need",
		}},
		{"approvals", []string{
			"agree", "Agree", "Agreed", "Approved", "I approve it",
			"Fully granted", "Sounds good", "Sounds reasonable", "Good",
			"Let’s continue",
		}},
		{"acknowledgements", []string{
			"Done", "Resolved", "Added", "That’s done", "Oh never mind",
		}},
		{"a verb pointing back at the last turn", []string{
			"Fix it all", "Fix them all", "merge them", "Merge them all",
			"merge both", "Do them", "Do all 3", "Do 2", "Do it yourself",
			"Check again", "Check it", "Run it", "Show it", "Test it",
			"Kill it", "Clear it", "Explain", "Improve", "repeat",
			"retry the merge", "All in parallel", "Both in parallel",
		}},
		{"misspellings of a continuation", []string{
			"proced", "ptroceed", "Proveed", "Poroceed", "Confinue",
			"continuie", "Ontinue", "Cconrinue", "\\continuie",
		}},
		{"stray keystrokes", []string{"A", "C", "gp"}},
		{"session management, not work", []string{"/compact", "/clear"}},
		{"text the harness injected", []string{
			"<bash-input>gh pr merge 443 --squash</bash-input>",
			"<command-message>init</command-message>",
			"Stop hook feedback:\n  run the tests",
		}},
		{"a file handed over with nothing said about it", []string{
			`@"/Users/me/.claude/uploads/abc/def.zip"`,
		}},
	} {
		for _, p := range group.inputs {
			if !IsContinuation(agentdx.Unit{Prompt: p}) {
				t.Errorf("IsContinuation(%q) = false, want true — %s", p, group.why)
			}
		}
	}
}

// The counterweight to every addition above. An instruction that names
// what to work on carries work of its own however short it is, and a
// lexicon that swallows these is worse than the length rule it replaced.
func TestExtendedLexiconStillLeavesRealWorkAlone(t *testing.T) {
	for _, p := range []string{
		"fix the parser", "add tests", "run the build",
		"Fix mnemos", "Fix 445", "lets fix rollops", "cut the release",
		"Cut 0.31.1", "repoint branch protection", "Quit ollama",
		"Remove the stale branches", "Delete the Hermes data",
		"harden the SBOM sequencing", "why nova?", "is rollops fixed?",
		"Ignore archived repos", "Start Wave 3", "check the remaining five",
		"Continue with roady roadmap", "Review Lippman for this",
		"/security-review", "/code-review high",
		"go through the parser", "status of the release pipeline",
	} {
		if IsContinuation(agentdx.Unit{Prompt: p}) {
			t.Errorf("IsContinuation(%q) = true, want false — it names the work", p)
		}
	}
}

// Typo tolerance is bounded so it cannot reach a real instruction: one
// edit, single-word instructions only, and nothing short enough that one
// edit reaches half the dictionary.
func TestMisspellingToleranceStaysNarrow(t *testing.T) {
	for _, p := range []string{"probe", "prune", "purge", "rerun", "print", "patch"} {
		if IsContinuation(agentdx.Unit{Prompt: p}) {
			t.Errorf("IsContinuation(%q) = true — typo tolerance reached a real instruction", p)
		}
	}
}
