package story

import (
	"strings"
	"unicode"

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
//
// The approvals, acknowledgements and progress questions below were added
// from 30 days of real history: every one of them titled a task that was
// plainly a continuation of the instruction before it.
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

	// Approvals. The operator is signing off on work already proposed.
	"agree": true, "i agree": true, "agreed": true, "approve": true,
	"approved": true, "i approve": true, "i approve it": true,
	"granted": true, "fully granted": true, "go for them": true,
	"sounds good": true, "sounds reasonable": true, "sounds awesome": true,
	"makes sense": true, "good": true, "great": true, "nice": true,
	"fine": true, "correct": true, "exactly": true, "right": true,
	"cool": true, "got it": true, "understood": true, "all good": true,
	"let's go": true, "lets go": true, "let's continue": true,
	"lets continue": true, "let's proceed": true, "lets proceed": true,

	// Acknowledgements: the operator reporting that something outside the
	// agent's reach is now true. A statement about the work in flight.
	"done": true, "resolved": true, "added": true, "that's done": true,
	"never mind": true, "forget it": true, "leave it": true, "skip it": true,

	// Contentless modifiers: the operator saying *how* the work already
	// asked for should be done, not asking for different work.
	"in parallel": true, "all in parallel": true, "both in parallel": true,
	"in sequence": true, "one at a time": true,

	// Progress questions. "Status" titled a task in a live run; asking how
	// the work is going is the clearest case there is of an instruction
	// that cannot be read without the work it asks about.
	"status": true, "what's next": true, "whats next": true,
	"what next": true, "anything else": true, "what else": true,
	"any blockers": true, "any blocker": true, "any gaps": true,
	"any questions": true, "any question": true, "any new finding": true,
	"any new findings": true, "all done": true, "all shipped": true,
	"where are we": true, "what do you need": true, "what's the status": true,
	"how's it going": true, "hows it going": true,
}

// continuationCommands are slash commands that manage the session rather
// than ask for work. `/compact` is the operator making room to carry on;
// the work either side of it is one task.
//
// Deliberately a short list rather than "any slash command": `/review` and
// `/security-review` are instructions like any other, and titling a task
// after one is right.
var continuationCommands = map[string]bool{
	"/compact": true, "/clear": true, "/cost": true, "/status": true,
	"/context": true, "/resume": true, "/memory": true, "/config": true,
}

// harnessPrefixes mark text the harness injected into the transcript
// rather than an instruction the operator typed: a shell command run in
// bash mode, a slash command's expansion, a hook speaking up. None of it
// is a request for work, so none of it can open or title a task.
var harnessPrefixes = []string{
	"<bash-input>", "<command-message>", "<command-name>",
	"<local-command-stdout>", "<local-command-caveat>",
	"stop hook feedback:",
}

// continuationOpeners are first words that bind an instruction to the one
// before it. A sentence beginning "and also…" or "actually, …" is
// grammatically about what preceded it.
//
// Matched as the first word with trailing punctuation stripped, not as a
// literal prefix: "actually revert that" and "actually, revert that" are
// the same instruction, and a prefix list has to carry both spellings of
// every opener to catch them.
//
// "let's" is deliberately absent: "lets fix rollops" is real work, and an
// opener rule cannot tell it from "let's continue". The few contentless
// spellings of it are exact entries in continuationWords instead.
var continuationOpeners = map[string]bool{
	"and": true, "also": true, "now": true, "then": true, "plus": true,
	"but": true, "so": true, "instead": true, "actually": true,
	"no": true, "nope": true, "yes": true, "ok": true, "okay": true,
	"oh": true, "sounds": true, "agreed": true, "approved": true, "thanks": true,
	"perfect": true, "exactly": true, "yep": true, "yeah": true,
}

// continuationVerbs and anaphors together catch the shape the exact list
// keeps missing: a verb whose only object points back at the last turn.
// "fix it all", "merge them all", "check again", "do the rest" cannot be
// read without the work in flight — which is the same reason "go" cannot.
//
// The rule fires only when *every* word is one or the other, so it cannot
// reach "fix the parser", "add tests" or "run the build": "parser",
// "tests" and "build" name work, and naming work is what makes an
// instruction substantive.
var continuationVerbs = map[string]bool{
	"go": true, "do": true, "fix": true, "merge": true, "ship": true,
	"run": true, "test": true, "check": true, "apply": true, "push": true,
	"proceed": true, "continue": true, "retry": true, "redo": true,
	"repeat": true, "recreate": true, "cut": true, "kill": true,
	"clear": true, "show": true, "explain": true, "execute": true,
	"split": true, "finish": true, "approve": true, "review": true,
	"improve": true, "remove": true, "delete": true, "revert": true,
	"undo": true, "try": true, "keep": true, "start": true, "handle": true,
}

// anaphors point back rather than naming anything. A short bare numeral
// counts: "do 2" and "fix all three" are only answerable against a list
// the agent just produced.
var anaphors = map[string]bool{
	"it": true, "that": true, "this": true, "them": true, "they": true,
	"those": true, "these": true, "all": true, "both": true, "each": true,
	"rest": true, "other": true, "others": true, "one": true, "two": true,
	"three": true, "four": true, "five": true, "again": true, "now": true,
	"then": true, "too": true, "as": true, "well": true, "also": true,
	"the": true, "and": true, "first": true, "next": true, "last": true,
	"more": true, "up": true, "on": true, "out": true, "in": true,
	"parallel": true, "yourself": true, "everything": true,
}

// There is deliberately no length rule. Brevity is not continuation:
// "fix the parser", "add tests" and "run the build" are all short and all
// real work. A first attempt here treated any instruction of three words
// or fewer as a continuation and swallowed exactly those. An instruction
// continues because of what it says, so the lexicon above is the whole
// mechanism — and being a list rather than a threshold, it can be read,
// argued with and extended when it misses.
//
// The one exception is a single token of one or two characters. "A", "C"
// and "gp" all titled tasks on real history; no instruction that carries
// work fits in two characters, so the exception cannot swallow one.
const strayKeystrokeLen = 2

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
	raw := strings.TrimSpace(u.Prompt)
	if raw == "" {
		// Without text there is nothing to judge. Fall back to treating it
		// as new work, so a scan that did not carry prompt text still
		// produces the same boundaries it always did.
		return false
	}
	if carriesNoWork(raw) {
		return true
	}
	p := normalise(raw)
	if p == "" {
		return true
	}
	if continuationWords[p] {
		return true
	}
	fields := strings.Fields(p)
	if len(fields) == 1 {
		if len([]rune(fields[0])) <= strayKeystrokeLen {
			return true
		}
		if isMisspeltContinuation(fields[0]) {
			return true
		}
	}
	if len(fields) > 1 && continuationOpeners[strings.TrimRight(fields[0], ",:;-")] {
		return true
	}
	return allBackReference(fields)
}

// carriesNoWork covers instructions that are not requests at all: a
// session-management command, text the harness injected, or a file
// dropped in with nothing said about it.
func carriesNoWork(raw string) bool {
	first := strings.TrimSpace(firstLine(raw))
	lower := strings.ToLower(first)
	if continuationCommands[lower] {
		return true
	}
	for _, p := range harnessPrefixes {
		if strings.HasPrefix(lower, p) {
			return true
		}
	}
	// A bare attachment: the operator handed over a file and said nothing.
	// Whatever the work is, this line does not state it.
	return strings.HasPrefix(first, "@\"") && strings.HasSuffix(first, "\"")
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// normalise lowercases, folds typographic apostrophes onto the ASCII one —
// "What’s next" and "what's next" are one instruction, and a lexicon that
// has to carry both spellings of every entry is a lexicon with holes — and
// strips the punctuation that decorates an instruction without changing it.
func normalise(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.NewReplacer("’", "'", "‘", "'", "“", "\"", "”", "\"").Replace(s)
	return strings.Trim(s, " .!?,:;-\\/*_\"'")
}

// allBackReference reports whether every word of the instruction either
// is a verb from the continuation list or points back at the last turn.
// A single bare verb qualifies — "fix", "merge" and "continue" all titled
// tasks — but a lone anaphor does not, since "it" on its own is not an
// instruction anyone typed.
func allBackReference(fields []string) bool {
	if len(fields) == 0 {
		return false
	}
	verbs := 0
	for _, f := range fields {
		w := strings.Trim(f, ",.:;-'\"")
		switch {
		case continuationVerbs[w]:
			verbs++
		case anaphors[w], isNumeral(w):
		default:
			return false
		}
	}
	return verbs > 0
}

// isNumeral reports whether a word is a position in a list the agent just
// produced. Capped at two digits on purpose: "do 2" points back, "fix 445"
// names an issue, and naming something is what makes an instruction carry
// work of its own.
func isNumeral(w string) bool {
	if w == "" || len(w) > 2 {
		return false
	}
	for _, r := range w {
		if !unicode.IsDigit(r) {
			return false
		}
	}
	return true
}

// isMisspeltContinuation catches the typo. "proced", "ptroceed",
// "continuie" and "cconrinue" all titled tasks on real history, and they
// are the same instruction as "proceed" and "continue" — an operator
// typing fast at the end of a turn.
//
// Bounded hard so it cannot reach real work: only a single-word
// instruction, only against single-word entries in the lexicon, and only
// within one edit — two only once the word is long enough that two edits
// no longer reach anything else.
func isMisspeltContinuation(w string) bool {
	rw := []rune(w)
	if len(rw) < 5 {
		return false
	}
	budget := 1
	if len(rw) >= 8 {
		budget = 2
	}
	for target := range continuationWords {
		if strings.ContainsRune(target, ' ') {
			continue
		}
		rt := []rune(target)
		if len(rt) < 5 || abs(len(rw)-len(rt)) > budget {
			continue
		}
		if editDistanceWithin(rw, rt, budget) {
			return true
		}
	}
	return false
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// editDistanceWithin reports whether a and b are within budget edits.
// Plain Levenshtein over two rows; the words involved are a handful of
// runes, so the cost is irrelevant and the clarity is not.
func editDistanceWithin(a, b []rune, budget int) bool {
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur[0] = i
		best := cur[0]
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min3(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
			if cur[j] < best {
				best = cur[j]
			}
		}
		if best > budget {
			return false
		}
		prev, cur = cur, prev
	}
	return prev[len(b)] <= budget
}

func min3(a, b, c int) int {
	m := a
	if b < m {
		m = b
	}
	if c < m {
		m = c
	}
	return m
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
