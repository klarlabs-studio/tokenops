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
		{"claude-opus-5-5", 1_000_000},
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

// catalogOf registers windows as the pricing catalog's answer for the
// duration of one test.
func catalogOf(t *testing.T, windows map[string]int64) {
	t.Helper()
	SetCatalogWindows(func() map[string]int64 { return windows })
	t.Cleanup(func() { SetCatalogWindows(nil) })
}

// Models the Claude table does not know are answered from the catalog's
// max_input_tokens, keyed by the bare model name, so a Gemini CLI model and
// a Pi model id with a provider prefix both size.
func TestContextWindowFallsBackToCatalog(t *testing.T) {
	catalogOf(t, map[string]int64{
		"gemini-2.5-pro":   1_048_576,
		"gemini-2.5-flash": 1_048_576,
		"glm-4.6":          200_000,
	})
	for _, tc := range []struct {
		model string
		want  int64
	}{
		{"gemini-2.5-pro", 1_048_576},
		{"Gemini-2.5-Pro", 1_048_576},
		{"google/gemini-2.5-flash", 1_048_576},
		{"openrouter/z-ai/glm-4.6", 200_000},
		{"vertex_ai/gemini-2.5-flash", 1_048_576},
	} {
		got, ok := ContextWindow(tc.model)
		if !ok || got != tc.want {
			t.Errorf("ContextWindow(%q) = %d, %v; want %d", tc.model, got, ok, tc.want)
		}
	}
}

// The Claude table stays authoritative: a catalog that disagrees about a
// Claude model does not move its window.
func TestClaudeTableOutranksCatalog(t *testing.T) {
	catalogOf(t, map[string]int64{"claude-sonnet-4-6": 999_999, "claude-haiku-4-5": 1})
	for model, want := range map[string]int64{"claude-sonnet-4-6": 200_000, "claude-haiku-4-5": 200_000} {
		if got, ok := ContextWindow(model); !ok || got != want {
			t.Errorf("ContextWindow(%q) = %d, %v; want the Claude table's %d", model, got, ok, want)
		}
	}
}

// A model in neither source stays unavailable, and so does every model
// when no catalog is registered.
func TestModelInNeitherSourceHasNoWindow(t *testing.T) {
	catalogOf(t, map[string]int64{"gemini-2.5-pro": 1_048_576})
	for _, m := range []string{"gemini-unknown", "openrouter/x/unknown-model", "gpt-5"} {
		if w, ok := ContextWindow(m); ok {
			t.Errorf("%q reported %d; it is in neither source", m, w)
		}
	}
	SetCatalogWindows(nil)
	if w, ok := ContextWindow("gemini-2.5-pro"); ok {
		t.Errorf("without a catalog, gemini-2.5-pro reported %d", w)
	}
}
