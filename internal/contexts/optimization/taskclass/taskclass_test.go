package taskclass

import "testing"

func body(msgs string) []byte {
	return []byte(`{"model":"m","messages":[` + msgs + `]}`)
}

func userMsg(text string) string   { return `{"role":"user","content":"` + text + `"}` }
func toolUse() string              { return `{"role":"assistant","content":[{"type":"tool_use","name":"Bash"}]}` }
func toolResult() string           { return `{"role":"user","content":[{"type":"tool_result","content":"ok"}]}` }
func assistantMsg(t string) string { return `{"role":"assistant","content":"` + t + `"}` }

// A turn that is almost entirely tool traffic with a terse instruction is
// mechanical: the model is executing a plan, not forming one. That is the
// cheapest thing to route down without losing anything the operator values.
func TestHighToolDensityShortPromptIsMechanical(t *testing.T) {
	msgs := userMsg("continue") + "," + toolUse() + "," + toolResult() + "," +
		toolUse() + "," + toolResult() + "," + toolUse() + "," + toolResult()
	got := Classify(body(msgs), Config{})
	if got.Class != Mechanical {
		t.Errorf("Class = %v, want Mechanical (signals: %+v)", got.Class, got)
	}
	if got.Reason == "" {
		t.Error("classification must explain itself")
	}
}

// A long, substantive prompt with little tool traffic is reasoning work.
// Routing it down is exactly the trade an operator does not want made for
// them silently.
func TestLongPromptLowToolDensityIsReasoning(t *testing.T) {
	long := ""
	for range 60 {
		long += "design the bounded context and justify the tradeoffs "
	}
	msgs := userMsg(long) + "," + assistantMsg("Here is the analysis.")
	got := Classify(body(msgs), Config{})
	if got.Class != Reasoning {
		t.Errorf("Class = %v, want Reasoning (signals: %+v)", got.Class, got)
	}
}

// Anything that is neither clearly mechanical nor clearly reasoning must
// come back Unknown so the caller leaves the model alone. A classifier
// that guesses is worse than one that abstains.
func TestAmbiguousTurnIsUnknown(t *testing.T) {
	msgs := userMsg("add a test for the retry path and run it") + "," + toolUse() + "," + toolResult()
	got := Classify(body(msgs), Config{})
	if got.Class != Unknown {
		t.Errorf("Class = %v, want Unknown for an ambiguous turn (signals: %+v)", got.Class, got)
	}
}

// An unparseable or non-chat body yields Unknown, never a confident call.
func TestUnparseableBodyIsUnknown(t *testing.T) {
	for _, b := range [][]byte{nil, []byte(""), []byte("not json"), []byte(`{"prompt":"x"}`)} {
		if got := Classify(b, Config{}); got.Class != Unknown {
			t.Errorf("Classify(%q) = %v, want Unknown", b, got.Class)
		}
	}
}

// The regenerate signal: an operator rejecting the previous answer is
// reasoning work by definition — they want a better result, not a cheaper
// one. It overrides an otherwise mechanical-looking shape.
func TestRegenerateSignalForcesReasoning(t *testing.T) {
	msgs := assistantMsg("done") + "," + userMsg("no, that's wrong, try again") + "," +
		toolUse() + "," + toolResult() + "," + toolUse() + "," + toolResult()
	got := Classify(body(msgs), Config{})
	if got.Class != Reasoning {
		t.Errorf("Class = %v, want Reasoning after a rejection (signals: %+v)", got.Class, got)
	}
}

// Thresholds are tunable; an operator who wants to route more aggressively
// can widen what counts as mechanical.
func TestThresholdsAreTunable(t *testing.T) {
	msgs := userMsg("add a test for the retry path and run it") + "," + toolUse() + "," + toolResult()
	strict := Classify(body(msgs), Config{})
	loose := Classify(body(msgs), Config{MaxMechanicalWords: 40, MinToolDensity: 0.3})
	if strict.Class == loose.Class {
		t.Errorf("tuning had no effect: strict=%v loose=%v", strict.Class, loose.Class)
	}
	if loose.Class != Mechanical {
		t.Errorf("loose Class = %v, want Mechanical", loose.Class)
	}
}
