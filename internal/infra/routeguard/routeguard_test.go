package routeguard

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/optimization/modeltier"
	"go.klarlabs.de/tokenops/internal/contexts/optimization/taskclass"
	"go.klarlabs.de/tokenops/internal/contexts/spend/spend"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

func catalog() *modeltier.Catalog {
	return modeltier.New(spend.Table{Currency: "USD", Rates: map[spend.Key]spend.Rate{
		{Provider: "anthropic", Model: "claude-haiku-4-5"}: {InputPerMillion: 1, OutputPerMillion: 5},
		{Provider: "anthropic", Model: "claude-sonnet-5"}:  {InputPerMillion: 2, OutputPerMillion: 10},
		{Provider: "anthropic", Model: "claude-opus-5"}:    {InputPerMillion: 5, OutputPerMillion: 25},
		{Provider: "anthropic", Model: "claude-fable-5-1"}: {InputPerMillion: 10, OutputPerMillion: 50},
	}}, nil)
}

func input(t *testing.T, prompt, model string, mode Mode) Input {
	t.Helper()
	return Input{
		Dir: filepath.Join(t.TempDir(), "nested", "state"), SessionID: "s1",
		Prompt: prompt, CurrentModel: model, Provider: eventschema.ProviderAnthropic,
		Mode: mode, Catalog: catalog(), Now: time.Now(),
		Candidates: []string{"claude-haiku-4-5", "claude-sonnet-5", "claude-opus-5", "claude-fable-5-1"},
	}
}

// The whole point: an operator picks a capable model once and every turn
// afterwards runs on it, including the retrieval that any cheap model
// would do faster.
func TestAdvisesDownForCheapWork(t *testing.T) {
	for _, tc := range []struct{ prompt, wantTo string }{
		{"show me the retention config", "claude-haiku-4-5"},
		{"research how prefix keys work and summarise", "claude-sonnet-5"},
	} {
		got := Evaluate(input(t, tc.prompt, "claude-opus-5", ModeAdvise))
		if !got.Advise {
			t.Fatalf("%q: no advice given", tc.prompt)
		}
		if got.To != tc.wantTo {
			t.Errorf("%q: To = %q, want %q", tc.prompt, got.To, tc.wantTo)
		}
		if got.From != "claude-opus-5" {
			t.Errorf("From = %q", got.From)
		}
	}
}

// Work that earns the model it is on gets no nagging.
func TestSilentWhenTheModelFits(t *testing.T) {
	got := Evaluate(input(t, "refactor the router across every provider", "claude-opus-5", ModeAdvise))
	if got.Advise {
		t.Errorf("advised on deep work: %+v", got)
	}
}

// Never route up. Suggesting a pricier model is a spending decision, and
// on a subscription it can walk an operator into a cap they did not
// choose — Fable is limited to half the weekly allowance on Max.
func TestNeverRoutesUp(t *testing.T) {
	got := Evaluate(input(t, "refactor the router across every provider", "claude-haiku-4-5", ModeAdvise))
	if got.Advise && got.To == "claude-fable-5-1" {
		t.Errorf("routed up to %q unasked", got.To)
	}
}

// A state directory that is never created is a hook that reports success
// and stores nothing; this fleet has shipped that bug before.
func TestCreatesItsStateDirectory(t *testing.T) {
	in := input(t, "show me the config", "claude-opus-5", ModeAdvise)
	Evaluate(in)
	if _, err := os.Stat(in.Dir); err != nil {
		t.Fatalf("state dir not created: %v", err)
	}
}

// A continuation carries the task's kind, so "go" after a research
// instruction is still research rather than an unknown that abstains.
func TestContinuationKeepsTheTaskKind(t *testing.T) {
	in := input(t, "research how the bus drops events", "claude-opus-5", ModeAdvise)
	first := Evaluate(in)
	if first.Kind != taskclass.KindResearch {
		t.Fatalf("first kind = %q", first.Kind)
	}
	in.Prompt = "go"
	if got := Evaluate(in); got.Kind != taskclass.KindResearch {
		t.Errorf("continuation kind = %q, want research", got.Kind)
	}
}

// Advice repeated every turn stops being read. It is argued once per
// kind per session and then left alone.
func TestArguesOncePerKind(t *testing.T) {
	in := input(t, "show me the retention config", "claude-opus-5", ModeAdvise)
	if !Evaluate(in).Advise {
		t.Fatal("no advice on the first turn")
	}
	in.Prompt = "show me the other config"
	if Evaluate(in).Advise {
		t.Error("advised twice for the same kind in one session")
	}
}

// Off means off.
func TestModeOffSaysNothing(t *testing.T) {
	if got := Evaluate(input(t, "show me the config", "claude-opus-5", ModeOff)); got.Advise {
		t.Errorf("advised while off: %+v", got)
	}
}

// Delegation is a stronger claim than advice and only applies to the
// kinds an operator allowed.
func TestDelegateOnlyForAllowedKinds(t *testing.T) {
	in := input(t, "show me the retention config", "claude-opus-5", ModeDelegate)
	in.AutoKinds = []taskclass.Kind{taskclass.KindResearch}
	if got := Evaluate(in); got.Delegate {
		t.Error("delegated a lookup that was not on the allowed list")
	}
	in2 := input(t, "research how the card handles prefixes", "claude-opus-5", ModeDelegate)
	in2.AutoKinds = []taskclass.Kind{taskclass.KindResearch}
	if got := Evaluate(in2); !got.Delegate {
		t.Error("did not delegate an allowed kind")
	}
}

// A model the catalog cannot place is not an invitation to guess.
func TestAbstainsOnUnplaceableModel(t *testing.T) {
	in := input(t, "show me the config", "big-pickle", ModeAdvise)
	if got := Evaluate(in); got.Advise {
		t.Errorf("advised from an unknown model: %+v", got)
	}
}

// Without a live model set the catalog ranks across the vendor's whole
// back catalogue, and the cheapest thing it can find is a model retired
// years ago. Recommending that is worse than saying nothing, so the
// guard abstains until it is told what is actually on offer.
func TestAbstainsWithoutAKnownModelSet(t *testing.T) {
	full := modeltier.New(spend.Table{Currency: "USD", Rates: map[spend.Key]spend.Rate{
		{Provider: "anthropic", Model: "claude-3-haiku"}:   {InputPerMillion: 0.25, OutputPerMillion: 1.25},
		{Provider: "anthropic", Model: "claude-haiku-4-5"}: {InputPerMillion: 1, OutputPerMillion: 5},
		{Provider: "anthropic", Model: "claude-opus-5"}:    {InputPerMillion: 5, OutputPerMillion: 25},
	}}, nil)
	in := input(t, "show me the retention config", "claude-opus-5", ModeAdvise)
	in.Catalog = full
	in.Candidates = nil
	if got := Evaluate(in); got.Advise {
		t.Errorf("advised %q with no model set configured", got.To)
	}
	// Told what is current, it advises the live cheap model.
	in.Candidates = []string{"claude-haiku-4-5", "claude-opus-5"}
	got := Evaluate(in)
	if !got.Advise || got.To != "claude-haiku-4-5" {
		t.Errorf("got %+v, want advice for claude-haiku-4-5", got)
	}
}
