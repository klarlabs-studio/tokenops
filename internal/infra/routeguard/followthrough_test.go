package routeguard

import (
	"fmt"
	"testing"

	"go.klarlabs.de/tokenops/internal/contexts/optimization/taskclass"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

func counter() func() string {
	n := 0
	return func() string { n++; return fmt.Sprint("advice-", n) }
}

// Advice is followed when a later turn runs below the tier it was given on.
func TestAdviceIsFollowedWhenTheSessionMovesDown(t *testing.T) {
	in := input(t, "show me the retention config", "claude-opus-5", ModeAdvise)
	in.NewID = counter()
	first := Evaluate(in)
	if first.OfferID != "advice-1" {
		t.Fatalf("OfferID = %q; want the new advice named", first.OfferID)
	}
	in.Prompt, in.CurrentModel = "and the other one", "claude-haiku-4-5"
	got := Evaluate(in)
	if len(got.Resolved) != 1 || !got.Resolved[0].Followed || got.Resolved[0].ID != "advice-1" {
		t.Fatalf("Resolved = %+v; want advice-1 followed", got.Resolved)
	}
	in.Prompt = "one more"
	if again := Evaluate(in); len(again.Resolved) != 0 {
		t.Errorf("advice resolved twice: %+v", again.Resolved)
	}
}

// Advice not followed within the window is ignored, once.
func TestAdviceIsIgnoredAfterTheWindow(t *testing.T) {
	in := input(t, "show me the retention config", "claude-opus-5", ModeAdvise)
	in.NewID = counter()
	Evaluate(in)
	resolved := make([]Resolution, 0, 1)
	for i := range AdviceWindow + 2 {
		in.Prompt = fmt.Sprint("show me file ", i)
		resolved = append(resolved, Evaluate(in).Resolved...)
	}
	if len(resolved) != 1 || resolved[0].Followed {
		t.Fatalf("Resolved = %+v; want one ignored", resolved)
	}
}

// Repeating advice at verbose does not open a second offer for the same
// kind while the first is still waiting.
func TestRepeatedAdviceIsOneOffer(t *testing.T) {
	in := input(t, "show me the retention config", "claude-opus-5", ModeAdvise)
	in.Verbosity, in.NewID = "verbose", counter()
	if Evaluate(in).OfferID == "" {
		t.Fatal("no offer on first advice")
	}
	in.Prompt = "show me the other config"
	got := Evaluate(in)
	if !got.Advise || got.OfferID != "" {
		t.Errorf("second turn: Advise=%v OfferID=%q; want advice without a new offer", got.Advise, got.OfferID)
	}
}

// Quieted kinds are withheld at normal and quiet, and still advised at
// verbose.
func TestQuietedAdviceIsWithheldUnlessVerbose(t *testing.T) {
	quiet := func(k taskclass.Kind) bool { return k == taskclass.KindLookup }
	in := input(t, "show me the retention config", "claude-opus-5", ModeAdvise)
	in.Quieted, in.NewID = quiet, counter()
	got := Evaluate(in)
	if got.Advise || !got.Quieted || got.OfferID != "" {
		t.Fatalf("normal: %+v; want withheld and marked quieted", got)
	}
	in = input(t, "show me the retention config", "claude-opus-5", ModeAdvise)
	in.Quieted, in.Verbosity = quiet, "verbose"
	if got := Evaluate(in); !got.Advise {
		t.Error("verbose: quieted advice withheld")
	}
}

// A subagent the agent asked to run below the advised-on tier follows the
// advice; one on the same tier does not.
func TestSubagentOnACheaperModelFollowsAdvice(t *testing.T) {
	in := input(t, "show me the retention config", "claude-opus-5", ModeAdvise)
	in.NewID = counter()
	Evaluate(in)
	if got := ObserveSubagent(in.Dir, in.SessionID, "opus", eventschema.ProviderAnthropic, catalog(), in.Candidates); len(got) != 0 {
		t.Fatalf("same-tier subagent followed advice: %+v", got)
	}
	got := ObserveSubagent(in.Dir, in.SessionID, "haiku", eventschema.ProviderAnthropic, catalog(), in.Candidates)
	if len(got) != 1 || !got[0].Followed {
		t.Fatalf("Resolved = %+v; want followed", got)
	}
	in.Prompt = "next"
	if again := Evaluate(in); len(again.Resolved) != 0 {
		t.Errorf("advice settled by the subagent resolved again: %+v", again.Resolved)
	}
}
