package story

import (
	"strings"

	"go.klarlabs.de/tokenops/internal/contexts/governance/agentdx"
)

// A pause is a poor boundary on its own. Agents run long, operators read
// the output, and the gap between two instructions on the same piece of
// work routinely exceeds any threshold worth setting: on 30 days of real
// history, splitting on a 10-minute gap alone made 82% of tasks a single
// instruction and titled 71% of them "Go" or "Proceed". A task that is
// usually one instruction is not a task.
//
// What actually separates continuing from starting is the instruction
// itself. "Go", "proceed", "no, like that" carry no work of their own —
// they are only meaningful against what came before, so whatever the gap,
// they continue it.

// continuationWords are instructions that exist only in reference to the
// work already in flight. Matched against the whole trimmed instruction,
// not as a prefix, so "go" continues and "go through the parser" does not.
var continuationWords = map[string]bool{
	"go": true, "go on": true, "go ahead": true, "go for it": true,
	"proceed": true, "continue": true, "carry on": true, "keep going": true,
	"yes": true, "y": true, "yep": true, "yeah": true, "ok": true, "okay": true,
	"sure": true, "please": true, "do it": true, "do that": true,
	"next": true, "more": true, "again": true, "retry": true, "try again": true,
	"fix it": true, "fix them": true, "fix that": true,
	"ship it": true, "merge it": true, "split": true, "apply": true,
	"looks good": true, "lgtm": true, "perfect": true, "thanks": true,
	"no": true, "nope": true, "stop": true, "wait": true, "hold on": true,
}

// continuationOpeners are first words that bind an instruction to the one
// before it. A sentence beginning "and also…" or "actually, …" is
// grammatically about what preceded it.
//
// Matched as the first word with trailing punctuation stripped, not as a
// literal prefix: "actually revert that" and "actually, revert that" are
// the same instruction, and a prefix list has to carry both spellings of
// every opener to catch them.
var continuationOpeners = map[string]bool{
	"and": true, "also": true, "now": true, "then": true, "plus": true,
	"but": true, "so": true, "instead": true, "actually": true,
	"no": true, "nope": true, "yes": true, "ok": true, "okay": true,
}

// There is deliberately no length rule. Brevity is not continuation:
// "fix the parser", "add tests" and "run the build" are all short and all
// real work. A first attempt here treated any instruction of three words
// or fewer as a continuation and swallowed exactly those. An instruction
// continues because of what it says, so the lexicon above is the whole
// mechanism — and being a list rather than a threshold, it can be read,
// argued with and extended when it misses.

// IsContinuation reports whether an instruction continues the work in
// flight rather than starting new work.
//
// A rejection is always a continuation: saying the last answer was wrong
// is a statement about work already underway, and it is the single
// strongest signal available because agentdx derives it from the
// transcript rather than from wording alone.
func IsContinuation(u agentdx.Unit) bool {
	if u.PromptRejects {
		return true
	}
	p := strings.ToLower(strings.TrimSpace(u.Prompt))
	if p == "" {
		// Without text there is nothing to judge. Fall back to treating it
		// as new work, so a scan that did not carry prompt text still
		// produces the same boundaries it always did.
		return false
	}
	p = strings.TrimRight(p, ".!?")
	if continuationWords[p] {
		return true
	}
	if fields := strings.Fields(p); len(fields) > 1 {
		if continuationOpeners[strings.TrimRight(fields[0], ",:;-")] {
			return true
		}
	}
	return false
}

// firstSubstantive returns the index of the first instruction in the task
// that carries work of its own, or 0 when every one is a continuation.
//
// A task opened by "Go" should not be titled "Go". The opening
// instruction is not always the defining one — it is merely the earliest.
func firstSubstantive(units []agentdx.Unit) int {
	for i, u := range units {
		if !IsContinuation(u) && strings.TrimSpace(u.Prompt) != "" {
			return i
		}
	}
	return 0
}
