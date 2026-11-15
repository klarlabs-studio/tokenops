package compactlever

import (
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/avoidable"
	coachcap "go.klarlabs.de/tokenops/internal/capability/coach"
	"go.klarlabs.de/tokenops/internal/config"
)

// The developer's derived context line sets where Claude Code and Codex
// compact when it is measured; the configured plan value stands when it is
// not, and a stale or implausibly small line is not believed.
func TestPlanUsesTheDerivedLineWhenMeasured(t *testing.T) {
	now := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	cfg := config.Config{}
	configuredClaude := coachcap.CompactAt(cfg, "claude-code:")
	configuredCodex := coachcap.CompactAt(cfg, "codex:")
	tests := []struct {
		name string
		line *avoidable.ContextLine
		age  time.Duration
		// derived is true when both agents should compact at the line.
		derived bool
		line64  int64
	}{
		{"no line measured: the configured values stand", nil, 0, false, 0},
		{"a measured line replaces both", &avoidable.ContextLine{Tokens: 120_000}, time.Hour, true, 120_000},
		{"a line below the floor is not believed", &avoidable.ContextLine{Tokens: 9_000}, time.Hour, false, 0},
		{"a stale snapshot is not believed", &avoidable.ContextLine{Tokens: 120_000}, 3 * avoidable.MaxSnapshotAge, false, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			if tt.line != nil {
				s := avoidable.Snapshot{ComputedAt: now.Add(-tt.age), Report: avoidable.Report{ContextLine: tt.line}}
				if err := avoidable.WriteSnapshot(dir, s); err != nil {
					t.Fatal(err)
				}
			}
			p := planFor(cfg, dir, now)
			wantClaude, wantCodex := configuredClaude, configuredCodex
			if tt.derived {
				wantClaude, wantCodex = tt.line64, tt.line64
			}
			if p.ClaudeCompactAt != wantClaude || p.CodexCompactAt != wantCodex {
				t.Errorf("plan = claude %d, codex %d; want claude %d, codex %d",
					p.ClaudeCompactAt, p.CodexCompactAt, wantClaude, wantCodex)
			}
		})
	}
}

// The configured plan value is the fallback when the line is not measured.
func TestPlanFallsBackToTheConfiguredPlanValue(t *testing.T) {
	cfg := config.Config{Coaching: config.CoachingConfig{ContextLimits: []config.ContextLimitConfig{
		{WorkflowPrefix: "claude-code:", CompactAtTokens: 300_000},
	}}}
	p := planFor(cfg, t.TempDir(), time.Now())
	if p.ClaudeCompactAt != 300_000 {
		t.Errorf("claude compacts at %d, want the configured 300000", p.ClaudeCompactAt)
	}
}
