package tokenizer

import (
	"testing"

	"go.klarlabs.de/tokenops/pkg/eventschema"
)

func TestTiktokenExactCounts(t *testing.T) {
	tk, err := NewOpenAITiktoken()
	if err != nil {
		t.Fatalf("load tiktoken: %v", err)
	}
	if tk.Provider() != eventschema.ProviderOpenAI {
		t.Errorf("provider = %q, want openai", tk.Provider())
	}
	if tk.CountText("") != 0 {
		t.Error("empty string must be 0 tokens")
	}
	// "hello world" is 2 tokens in every GPT BPE (o200k included).
	if got := tk.CountText("hello world"); got != 2 {
		t.Errorf(`CountText("hello world") = %d, want 2`, got)
	}
}

// TestRegistryUsesExactOpenAITokenizer proves NewRegistry installs the exact
// BPE tokenizer over the heuristic for OpenAI, and that they genuinely differ
// on a dense code payload (the case the heuristic is worst at).
func TestRegistryUsesExactOpenAITokenizer(t *testing.T) {
	reg := NewRegistry()
	exact, err := NewOpenAITiktoken()
	if err != nil {
		t.Fatalf("load tiktoken: %v", err)
	}
	code := "func (t tiktokenCodec) CountText(s string) int { return len(s) / 4 }\n" +
		`{"messages":[{"role":"user","content":"x"}],"max_tokens":128}`

	regN, err := reg.CountText(eventschema.ProviderOpenAI, code)
	if err != nil {
		t.Fatalf("registry count: %v", err)
	}
	if want := exact.CountText(code); regN != want {
		t.Errorf("registry openai count = %d, want exact %d (registry not using tiktoken)", regN, want)
	}

	// The exact count must differ from the old char/4-style heuristic on
	// this dense payload — otherwise the upgrade bought nothing.
	heur := NewOpenAITokenizer().CountText(code)
	if regN == heur {
		t.Errorf("exact (%d) == heuristic (%d); expected divergence on code/JSON", regN, heur)
	}
}
