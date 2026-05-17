package prompts

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ComputeTurnStats sums assistant-turn tokens and prices them at
// the claude-opus-4-7 cache-aware rate. Verify the rollup on a
// known-shape JSONL.
func TestComputeTurnStatsClaudeCode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sess.jsonl")
	// Three assistant turns: 100 uncached input + 50 output each,
	// plus 1000 cache_read each. Total input bundled = 1100 per
	// turn; total output = 50; cached = 1000.
	lines := []string{
		`{"type":"user","sessionId":"s","timestamp":"2026-05-16T10:00:00Z","message":{"content":"hi"}}`,
		`{"type":"assistant","sessionId":"s","message":{"id":"m1","usage":{"input_tokens":100,"output_tokens":50,"cache_read_input_tokens":1000}}}`,
		`{"type":"assistant","sessionId":"s","message":{"id":"m2","usage":{"input_tokens":100,"output_tokens":50,"cache_read_input_tokens":1000}}}`,
		`{"type":"assistant","sessionId":"s","message":{"id":"m3","usage":{"input_tokens":100,"output_tokens":50,"cache_read_input_tokens":1000}}}`,
	}
	if err := os.WriteFile(path, []byte(joinNL(lines)), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	stats, err := ComputeTurnStats(ExtractOptions{Root: dir, Source: SourceClaudeCode})
	if err != nil {
		t.Fatalf("ComputeTurnStats: %v", err)
	}
	if stats.TotalTurns != 3 {
		t.Errorf("turns = %d; want 3", stats.TotalTurns)
	}
	if stats.AvgInputTokens != 1100 {
		t.Errorf("AvgInput = %.0f; want 1100", stats.AvgInputTokens)
	}
	if stats.AvgCachedTokens != 1000 {
		t.Errorf("AvgCached = %.0f; want 1000", stats.AvgCachedTokens)
	}
	if stats.AvgOutputTokens != 50 {
		t.Errorf("AvgOutput = %.0f; want 50", stats.AvgOutputTokens)
	}
	// uncached 100 × $15/M + cached 1000 × $1.50/M + output 50 × $75/M
	// = 0.0015 + 0.0015 + 0.00375 ≈ 0.00675
	if stats.AvgCostUSD < 0.006 || stats.AvgCostUSD > 0.008 {
		t.Errorf("AvgCost = %.6f; want ~0.00675", stats.AvgCostUSD)
	}
}

// Assistant turns with zero usage (tool-result echoes) must be
// dropped from the rollup — they have no real cost.
func TestComputeTurnStatsSkipsZeroUsage(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sess.jsonl")
	lines := []string{
		`{"type":"assistant","sessionId":"s","message":{"id":"m0","usage":{"input_tokens":0,"output_tokens":0,"cache_read_input_tokens":0}}}`,
		`{"type":"assistant","sessionId":"s","message":{"id":"m1","usage":{"input_tokens":100,"output_tokens":50}}}`,
	}
	if err := os.WriteFile(path, []byte(joinNL(lines)), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	stats, err := ComputeTurnStats(ExtractOptions{Root: dir, Source: SourceClaudeCode})
	if err != nil {
		t.Fatalf("ComputeTurnStats: %v", err)
	}
	if stats.TotalTurns != 1 {
		t.Errorf("turns = %d; want 1 (zero-usage line dropped)", stats.TotalTurns)
	}
}

// ProjectSavings multiplies the per-turn averages by the
// per-recommendation turn count and projects tokens / $ / hours.
func TestProjectSavings(t *testing.T) {
	stats := TurnStats{
		TotalTurns:      10,
		AvgInputTokens:  1000,
		AvgOutputTokens: 100,
		AvgCostUSD:      0.005,
		AvgSeconds:      45,
	}
	rec := Recommendation{EstimatedMonthlyTurnsSaved: 200}
	s := ProjectSavings(rec, stats)
	if s.Turns != 200 {
		t.Errorf("turns = %d", s.Turns)
	}
	// (1000 + 100) × 200 = 220,000 tokens
	if s.Tokens != 220_000 {
		t.Errorf("tokens = %d; want 220000", s.Tokens)
	}
	// 0.005 × 200 = $1.00
	if s.CostUSD < 0.99 || s.CostUSD > 1.01 {
		t.Errorf("cost = %.4f", s.CostUSD)
	}
	// 45s × 200 = 9000s = 2.5h
	if s.HoursSaved < 2.4 || s.HoursSaved > 2.6 {
		t.Errorf("hours = %.2f", s.HoursSaved)
	}
}

// Zero-turn stats produce zero savings without dividing by zero.
func TestProjectSavingsEmpty(t *testing.T) {
	s := ProjectSavings(Recommendation{EstimatedMonthlyTurnsSaved: 100}, TurnStats{})
	if s.Tokens != 0 || s.CostUSD != 0 || s.HoursSaved != 0 {
		t.Errorf("expected zeros; got %+v", s)
	}
}

func joinNL(lines []string) string {
	return strings.Join(lines, "\n")
}
