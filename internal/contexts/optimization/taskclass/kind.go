package taskclass

import "strings"

// Kind is what an instruction asks for, as opposed to how long it is.
//
// The length rule this sits beside calls only the extremes — a terse
// instruction over tool traffic is execution, a very long one is
// reasoning — and abstains on everything between. On one real machine
// that middle band held a third of all instructions, so the classifier
// was silent about the turns an operator spends most of their day on.
//
// Kind reads the request instead. It is deliberately a small vocabulary
// of opening verbs rather than anything cleverer: a rule an operator can
// read and predict is worth more here than one that is right slightly
// more often and cannot be argued with.
type Kind string

// The kinds, cheapest work first.
const (
	KindUnknown Kind = "unknown"
	// KindLookup is retrieval: find it, show it, list it. High volume,
	// no judgement, and the work most wasted on an expensive model.
	KindLookup Kind = "lookup"
	// KindResearch is reading widely and reporting back. Long to
	// describe and shallow to execute, which is exactly why a rule
	// keyed on instruction length mistakes it for hard work.
	KindResearch Kind = "research"
	// KindEdit is a routine change to known code.
	KindEdit Kind = "edit"
	// KindDeep is the work a vendor reserves its most capable model
	// for: refactors that cross a codebase, debugging that has already
	// resisted an attempt, and decisions about how something should be
	// built.
	KindDeep Kind = "deep"
)

// Tier-ordering helpers keep the mapping in one place rather than spread
// across callers that might disagree about what "research" deserves.
var kindOrder = map[Kind]int{
	KindUnknown: 0, KindLookup: 1, KindResearch: 2, KindEdit: 3, KindDeep: 4,
}

// AtLeast reports whether k is at least as demanding as other.
func (k Kind) AtLeast(other Kind) bool { return kindOrder[k] >= kindOrder[other] }

// Opening verbs and phrases per kind. Ordered most specific first: a
// phrase that names the work outranks a bare verb that merely starts the
// sentence.
var (
	deepMarkers = []string{
		"refactor", "redesign", "re-architect", "architect", "rearchitect",
		"debug", "diagnose", "root cause", "root-cause", "trace down",
		"design", "decide", "choose between", "trade-off", "tradeoff",
		"why is this failing", "figure out why",
	}
	researchMarkers = []string{
		"research", "investigate", "look into", "compare", "evaluate",
		"summarise", "summarize", "explain how", "explain why",
		"how does", "why does", "survey", "review the",
	}
	lookupMarkers = []string{
		"what does", "what is", "where is", "where does", "which",
		"show", "list", "find", "grep", "read", "print", "cat ",
		"look up", "check whether", "check if",
	}
	editMarkers = []string{
		"add", "fix", "change", "update", "rename", "implement", "write",
		"remove", "delete", "move", "extract", "inline", "bump", "wire",
	}
)

// ClassifyKind reads an instruction and says what kind of work it asks
// for, or Unknown when nothing in it does.
//
// A rejection outranks everything: an operator saying the last answer
// was wrong wants a better one, not a cheaper one, whatever verb the
// sentence happens to open with.
func ClassifyKind(instruction string) Kind {
	t := strings.ToLower(strings.TrimSpace(instruction))
	if t == "" {
		return KindUnknown
	}
	if isRejection(t) {
		return KindDeep
	}
	// A marker that opens the instruction describes the whole request;
	// the same word later in the sentence is usually describing its
	// object ("add a test for the research reader" is an edit).
	if k, ok := leadingMarker(t); ok {
		return k
	}
	// Nothing opened it, so fall back to naming the work anywhere in the
	// sentence, most demanding first so a mention of the hard thing is
	// never lost to a cheaper word later on.
	for _, m := range deepMarkers {
		if strings.Contains(t, m) {
			return KindDeep
		}
	}
	for _, m := range researchMarkers {
		if strings.Contains(t, m) {
			return KindResearch
		}
	}
	return KindUnknown
}

// leadingMarker matches the markers against the start of the
// instruction, checking every kind and preferring the longest match so
// "look into" is research rather than the "look up" prefix of lookup.
func leadingMarker(t string) (Kind, bool) {
	best, bestLen := KindUnknown, 0
	for kind, markers := range map[Kind][]string{
		KindDeep:     deepMarkers,
		KindResearch: researchMarkers,
		KindLookup:   lookupMarkers,
		KindEdit:     editMarkers,
	} {
		for _, m := range markers {
			if strings.HasPrefix(t, m) && len(m) > bestLen {
				best, bestLen = kind, len(m)
			}
		}
	}
	return best, bestLen > 0
}

// continuations are the ways an operator says "keep going" without
// restating the work. They carry no intent of their own, which is why a
// per-prompt classifier goes quiet across most of a real session.
var continuations = map[string]bool{
	"go": true, "continue": true, "proceed": true, "next": true,
	"do it": true, "go ahead": true, "carry on": true, "keep going": true,
	"resume": true, "more": true, "again": true, "yes": true, "y": true,
	"yep": true, "yeah": true, "ok": true, "okay": true, "sure": true,
	"please do": true, "agreed": true, "sounds good": true, "that works": true,
}

// KindForTurn is the classifier a caller should use per turn: it reads
// the instruction, and falls back to the kind already established for
// the task when the instruction is only telling it to continue.
//
// prev is the kind of the work in flight, KindUnknown when none has been
// established. An instruction that names new work replaces it; a
// continuation extends it; a rejection escalates, because an operator
// saying the last answer was wrong wants a better one whatever the task
// was before.
func KindForTurn(instruction string, prev Kind) Kind {
	t := strings.ToLower(strings.TrimSpace(instruction))
	t = strings.TrimRight(t, ".!? ")
	if t == "" {
		return prev
	}
	if isRejection(t) {
		return KindDeep
	}
	if continuations[t] {
		return prev
	}
	if k := ClassifyKind(instruction); k != KindUnknown {
		return k
	}
	return prev
}
