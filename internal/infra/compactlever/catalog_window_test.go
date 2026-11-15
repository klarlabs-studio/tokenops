package compactlever

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/spend/pricing"
	"go.klarlabs.de/tokenops/internal/contexts/spend/spend"
)

// Gemini CLI and Pi are sized from the catalog's input window for models
// the Claude table does not know. This runs the real chain end to end: a
// refreshed catalog on disk, the spend window lookup, and the writers, all
// under an isolated HOME and XDG_CONFIG_HOME.
func TestGeminiAndPiSetFromCatalogWindowEndToEnd(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	t.Setenv("GEMINI_CLI_HOME", home)
	t.Setenv("PI_CODING_AGENT_DIR", filepath.Join(home, ".pi", "agent"))
	t.Setenv("PI_CODING_AGENT_SESSION_DIR", "")
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, ".claude"))
	t.Setenv("CODEX_HOME", filepath.Join(home, ".codex"))

	// A refresh has stored the catalog's windows in the default pricing dir.
	snap := pricing.Snapshot{
		Source:         pricing.LiteLLMSourceName,
		FetchedAt:      time.Date(2026, 11, 14, 0, 0, 0, 0, time.UTC),
		Rates:          map[string]pricing.Rate{},
		ContextWindows: map[string]int64{"gemini-2.5-pro": 1_048_576, "glm-4.6": 200_000},
	}
	if _, err := pricing.SaveSnapshot(pricing.ResolveDir(""), snap); err != nil {
		t.Fatal(err)
	}

	p, err := DefaultPaths()
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{p.GeminiSettings, p.PiSettings} {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	write(t, p.GeminiSettings, `{"model": {"name": "gemini-2.5-pro"}}`)

	plan := Plan{
		GeminiCompactAt: 120_000,
		PiCompactAt:     100_000,
		PiModels:        []string{"openrouter/z-ai/glm-4.6"},
		Window:          spend.ContextWindow,
	}
	rs, err := Apply(p, plan, time.Now())
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if g := byClient(rs, ClientGemini); len(g) != 1 || g[0].Status != StatusApplied {
		t.Fatalf("gemini = %+v; want applied", g)
	}
	if pi := byClient(rs, ClientPi); len(pi) != 1 || pi[0].Status != StatusApplied {
		t.Fatalf("pi = %+v; want applied", pi)
	}

	model := jsonOf(t, p.GeminiSettings)["model"].(map[string]any)
	// 120,000 of 1,048,576 is 0.114, written to two places as Gemini CLI does.
	if model["compressionThreshold"] != 0.11 {
		t.Errorf("gemini compressionThreshold = %v; want 0.11", model["compressionThreshold"])
	}
	ov := jsonOf(t, p.PiSettings)["compaction"].(map[string]any)["modelOverrides"].(map[string]any)
	entry, _ := ov["openrouter/z-ai/glm-4.6"].(map[string]any)
	// The window less the measured line: 200,000 - 100,000.
	if entry["reserveTokens"] != float64(100_000) {
		t.Errorf("pi reserveTokens = %v; want 100000", entry["reserveTokens"])
	}
}
