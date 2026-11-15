package pricing

import (
	"encoding/json"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/spend/spend"
)

// litellmWindows is a LiteLLM body with the window fields that matter:
// max_input_tokens is the input limit, max_tokens the output limit.
const litellmWindows = `{
  "sample_spec": {"max_tokens": "set to max_output_tokens if provider specifies it", "max_input_tokens": "max input tokens"},
  "gemini/gemini-2.5-pro": {"litellm_provider": "gemini", "mode": "chat",
    "input_cost_per_token": 0.00000125, "output_cost_per_token": 0.00001,
    "max_input_tokens": 1048576, "max_output_tokens": 65536, "max_tokens": 65536},
  "openrouter/google/gemini-2.5-pro": {"litellm_provider": "openrouter", "mode": "chat",
    "input_cost_per_token": 0.00000125, "output_cost_per_token": 0.00001,
    "max_input_tokens": 500000, "max_output_tokens": 65536},
  "openrouter/z-ai/glm-4.6": {"litellm_provider": "openrouter", "mode": "chat",
    "input_cost_per_token": 0.0000006, "output_cost_per_token": 0.0000022,
    "max_input_tokens": 200000},
  "legacy-output-only": {"litellm_provider": "openai", "mode": "chat",
    "input_cost_per_token": 0.000001, "output_cost_per_token": 0.000002,
    "max_tokens": 8192},
  "gemini/gemini-embedding-001": {"litellm_provider": "gemini", "mode": "embedding",
    "input_cost_per_token": 0.00000015, "max_input_tokens": 2048}
}`

func TestParseLiteLLMReadsInputWindows(t *testing.T) {
	snap, err := ParseLiteLLM([]byte(litellmWindows), fixtureURL, time.Now())
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	cases := map[string]int64{
		// The direct vendor id beats a gateway's nested id for one name.
		"gemini-2.5-pro": 1_048_576,
		"glm-4.6":        200_000,
	}
	for model, want := range cases {
		if got := snap.ContextWindows[model]; got != want {
			t.Errorf("%s window = %d, want %d", model, got, want)
		}
	}
	if w, ok := snap.ContextWindows["legacy-output-only"]; ok {
		t.Errorf("max_tokens (an output limit) became an input window: %d", w)
	}
	if w, ok := snap.ContextWindows["gemini-embedding-001"]; ok {
		t.Errorf("an embedding model became a chat window: %d", w)
	}
}

func TestCombinedMergesContextWindows(t *testing.T) {
	a := Snapshot{Source: "a", Rates: map[string]Rate{}, ContextWindows: map[string]int64{"gemini-2.5-pro": 1}}
	b := Snapshot{Source: "b", Rates: map[string]Rate{}, ContextWindows: map[string]int64{"glm-4.6": 2}}
	got, err := Combined{fixedSource{"a", a}, fixedSource{"b", b}}.Fetch(t.Context())
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if got.ContextWindows["gemini-2.5-pro"] != 1 || got.ContextWindows["glm-4.6"] != 2 {
		t.Errorf("combined windows = %v", got.ContextWindows)
	}
}

// The catalog answers for the models it names once a refresh has saved
// them, and a later refresh replaces an earlier answer.
func TestContextWindowsFollowRefresh(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := ""
	save := func(at time.Time, w int64) {
		t.Helper()
		s := Snapshot{Source: "litellm", FetchedAt: at, Rates: map[string]Rate{}, ContextWindows: map[string]int64{"gemini-2.5-pro": w}}
		if _, err := SaveSnapshot(dir, s); err != nil {
			t.Fatalf("save: %v", err)
		}
	}
	window := func() (int64, bool) { return spend.ContextWindow("gemini-2.5-pro") }

	if w, ok := window(); ok {
		t.Fatalf("before any refresh, window = %d", w)
	}
	save(time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC), 1_000_000)
	if w, ok := window(); !ok || w != 1_000_000 {
		t.Fatalf("after first refresh, window = %d, %v", w, ok)
	}
	save(time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC), 1_048_576)
	if w, ok := window(); !ok || w != 1_048_576 {
		t.Fatalf("after second refresh, window = %d, %v; the newer catalog must win", w, ok)
	}
}

// A snapshot written before windows existed reads back with none.
func TestSnapshotWithoutWindowsStillLoads(t *testing.T) {
	var s Snapshot
	if err := json.Unmarshal([]byte(`{"source":"litellm","rates":{}}`), &s); err != nil || s.ContextWindows != nil {
		t.Fatalf("old snapshot: %v, windows %v", err, s.ContextWindows)
	}
}
