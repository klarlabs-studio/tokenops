package spend

import "testing"

// Anthropic documents Claude 4.6 and later as carrying the full 1M token
// context window at standard pricing. The maintainer's own transcripts
// confirm it: the largest turn observed on claude-opus-5 was 999,947
// tokens, which is the ceiling being touched rather than approached.
func TestContextWindowForCurrentModels(t *testing.T) {
	for _, tc := range []struct {
		model string
		want  int64
	}{
		{"claude-opus-5", 1_000_000},
		{"claude-opus-5[1m]", 1_000_000},
		{"claude-opus-4-8", 1_000_000},
		{"claude-opus-4-6", 1_000_000},
		{"claude-sonnet-5", 1_000_000},
		{"claude-fable-5", 1_000_000},
		{"claude-opus-4-5", 200_000},
		{"claude-sonnet-4-5", 200_000},
		{"claude-haiku-4-5", 200_000},
	} {
		got, ok := ContextWindow(tc.model)
		if !ok {
			t.Errorf("%s: no window known", tc.model)
			continue
		}
		if got != tc.want {
			t.Errorf("%s window = %d, want %d", tc.model, got, tc.want)
		}
	}
}

// An unrecognised model reports no window rather than a plausible
// default. A percentage computed against a guessed denominator is worse
// than no percentage: it looks authoritative and is not.
func TestUnknownModelHasNoWindow(t *testing.T) {
	for _, m := range []string{"", "gpt-5", "some-future-model", "<synthetic>"} {
		if w, ok := ContextWindow(m); ok {
			t.Errorf("%q reported a window of %d; it should report none", m, w)
		}
	}
}

// An explicit [1m] suffix wins regardless of what the family would give,
// because it is the operator telling us which variant they enabled.
func TestExplicitOneMillionSuffix(t *testing.T) {
	got, ok := ContextWindow("claude-haiku-4-5[1m]")
	if !ok || got != 1_000_000 {
		t.Errorf("got %d ok=%v, want the explicit 1M variant", got, ok)
	}
}
