package optimizer_test

import (
	"strings"
	"testing"

	"go.klarlabs.de/tokenops/internal/contexts/measurement"
	"go.klarlabs.de/tokenops/internal/contexts/optimization/optimizer"
	"go.klarlabs.de/tokenops/internal/contexts/prompts/tokenizer"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

func chatBody(text string) []byte {
	return []byte(`{"messages":[{"role":"user","content":"` + text + `"}]}`)
}

// Three different outcomes used to return the same int64: a counted
// saving, a counted no-saving, and a failure to count anything. The
// third is the one that matters — a savings figure nobody could verify
// presented as one that was.
func TestSavingsFromATokenizerAreMeasured(t *testing.T) {
	tk := tokenizer.NewRegistry()
	before := chatBody(strings.Repeat("hello world ", 50))
	after := chatBody("hello world")

	v := optimizer.EstimateTokenSavingsValue(tk, eventschema.ProviderOpenAI, before, after, 500)

	if v.Quality() != measurement.QualityMeasured {
		t.Errorf("quality = %q, want measured", v.Quality())
	}
	amount, ok := v.Amount()
	if !ok || amount <= 0 {
		t.Errorf("amount = %v, ok = %v, want a positive count", amount, ok)
	}
}

// Without a tokenizer the number is bytes/4. That is a guess, and it
// must say so — the routing and coaching surfaces that read it decide
// whether to act on the figure.
func TestSavingsWithoutATokenizerAreEstimated(t *testing.T) {
	v := optimizer.EstimateTokenSavingsValue(nil, eventschema.ProviderOpenAI,
		chatBody("aaa"), chatBody("a"), 400)

	if v.Quality() != measurement.QualityEstimated {
		t.Errorf("quality = %q, want estimated", v.Quality())
	}
	if amount, ok := v.Amount(); !ok || amount != 100 {
		t.Errorf("amount = %v, ok = %v, want 100 (400 bytes / 4)", amount, ok)
	}
	if !strings.Contains(v.Caveat(), "bytes") {
		t.Errorf("the caveat does not name the heuristic: %q", v.Caveat())
	}
}

// A tokenizer that counted and found nothing removed is a real answer:
// zero tokens were saved. It must not be confused with the case below.
func TestMeasuredNoSavingIsKnown(t *testing.T) {
	tk := tokenizer.NewRegistry()
	same := chatBody("identical content")

	v := optimizer.EstimateTokenSavingsValue(tk, eventschema.ProviderOpenAI, same, same, 0)

	amount, ok := v.Amount()
	if !ok {
		t.Fatal("a measured no-saving was reported as unknown")
	}
	if amount != 0 {
		t.Errorf("amount = %v, want 0", amount)
	}
	if v.Quality() != measurement.QualityMeasured {
		t.Errorf("quality = %q, want measured", v.Quality())
	}
}

// No tokenizer and nothing to fall back on is the case that used to
// return 0 and look exactly like the test above: a confident claim that
// the optimizer saved nothing, made by code that could not count.
func TestNoTokenizerAndNoFallbackIsUnknown(t *testing.T) {
	v := optimizer.EstimateTokenSavingsValue(nil, eventschema.ProviderOpenAI,
		chatBody("a"), chatBody("a"), 0)

	if v.Known() {
		t.Errorf("an uncountable saving reported %v", v.AmountOr(-1))
	}
	if !strings.Contains(v.Caveat(), "tokenizer") {
		t.Errorf("the caveat does not say why: %q", v.Caveat())
	}
}

// The legacy int64 entry point keeps its behaviour so the three
// optimizers that call it are unaffected until they migrate.
func TestLegacyEntryPointStillReturnsTheSameInt(t *testing.T) {
	tk := tokenizer.NewRegistry()
	before := chatBody(strings.Repeat("hello world ", 50))
	after := chatBody("hello world")

	legacy := optimizer.EstimateTokenSavings(tk, eventschema.ProviderOpenAI, before, after, 500)
	v := optimizer.EstimateTokenSavingsValue(tk, eventschema.ProviderOpenAI, before, after, 500)

	if got := int64(v.AmountOr(0)); got != legacy {
		t.Errorf("value = %d but legacy = %d", got, legacy)
	}
}

// A negative fallback is nonsense input, not a negative saving.
func TestNegativeFallbackIsUnknownNotZero(t *testing.T) {
	v := optimizer.EstimateTokenSavingsValue(nil, eventschema.ProviderOpenAI,
		chatBody("a"), chatBody("a"), -10)
	if v.Known() {
		t.Errorf("a negative fallback produced %v", v.AmountOr(-1))
	}
}
