package taskclass

import "testing"

// The length rule calls only the extremes: five words or fewer with tool
// traffic is execution, eighty or more is reasoning, and everything
// between is Unknown. On one real machine that middle band was a third
// of every instruction, so the classifier said nothing about the turns
// an operator actually spends their day on.
//
// Kind reads what the instruction asks for instead of how long it is.
func TestKindReadsIntentNotLength(t *testing.T) {
	for _, tc := range []struct {
		instruction string
		want        Kind
	}{
		// Retrieval: cheap, high volume, no judgement required.
		{"what does the poller do", KindLookup},
		{"show me the retention config", KindLookup},
		{"find where the bus drops events", KindLookup},
		{"list the enabled readers", KindLookup},

		// Research: long to describe, shallow to execute. The case the
		// length rule could never call.
		{"research how the rate card handles prefix keys and summarise the findings", KindResearch},
		{"investigate why opencode events are missing and explain what you find", KindResearch},
		{"compare our approach to the alternatives", KindResearch},

		// Routine change.
		{"add a test for the free model case", KindEdit},
		{"fix the lint error in the poller", KindEdit},
		{"rename this field to CostPerMillion", KindEdit},

		// The three things the vendor reserves its most capable model
		// for: cross-cutting refactors, hard debugging, architecture.
		{"refactor the router across every provider", KindDeep},
		{"debug why the daemon deadlocks on restart", KindDeep},
		{"design the retention policy model", KindDeep},
		{"decide whether to keep the proxy", KindDeep},
	} {
		if got := ClassifyKind(tc.instruction); got != tc.want {
			t.Errorf("ClassifyKind(%q) = %q, want %q", tc.instruction, got, tc.want)
		}
	}
}

// An instruction that rejects the previous answer wants a better answer,
// not a cheaper one. This outranks every other signal, including a verb
// that would otherwise read as cheap work.
func TestRejectionIsNeverCheap(t *testing.T) {
	for _, s := range []string{
		"no, that's wrong — show me the config again",
		"that doesn't work, find the actual cause",
	} {
		if got := ClassifyKind(s); got != KindDeep {
			t.Errorf("ClassifyKind(%q) = %q, want deep", s, got)
		}
	}
}

// Silence is the correct answer when nothing in the instruction says
// what kind of work it is. Routing a turn down on a guess is the trade
// an operator did not ask anyone to make for them.
func TestKindAbstains(t *testing.T) {
	for _, s := range []string{"", "proceed", "go", "ok", "hmm"} {
		if got := ClassifyKind(s); got != KindUnknown {
			t.Errorf("ClassifyKind(%q) = %q, want unknown", s, got)
		}
	}
}

// The verb that opens an instruction carries more weight than one buried
// in its object: "add a test for the research reader" is an edit, not
// research.
func TestLeadingVerbWins(t *testing.T) {
	if got := ClassifyKind("add a test for the research reader"); got != KindEdit {
		t.Errorf("got %q, want edit", got)
	}
	if got := ClassifyKind("show me the refactor plan"); got != KindLookup {
		t.Errorf("got %q, want lookup", got)
	}
}

// Most instructions are not task statements. On one real machine 56% of
// them were five words or fewer and 27% were a bare "go" or "proceed":
// the operator continuing work already in flight. Those carry no intent
// of their own, so classifying each prompt in isolation answers Unknown
// for most of a session.
//
// The kind therefore belongs to the task, and a continuation inherits it.
func TestContinuationInheritsTheTaskKind(t *testing.T) {
	for _, s := range []string{"go", "Proceed", "continue", "do it", "yes", "ok", "next"} {
		if got := KindForTurn(s, KindResearch); got != KindResearch {
			t.Errorf("KindForTurn(%q, research) = %q, want research", s, got)
		}
	}
	// An instruction that names new work replaces the inherited kind
	// rather than extending the old one.
	if got := KindForTurn("refactor the router across providers", KindLookup); got != KindDeep {
		t.Errorf("got %q, want deep: new work overrides the inherited kind", got)
	}
	// With nothing to inherit, silence stays silence.
	if got := KindForTurn("go", KindUnknown); got != KindUnknown {
		t.Errorf("got %q, want unknown", got)
	}
	// A rejection escalates even mid-task: the operator wants better,
	// not cheaper.
	if got := KindForTurn("no, that's wrong", KindLookup); got != KindDeep {
		t.Errorf("got %q, want deep", got)
	}
}
