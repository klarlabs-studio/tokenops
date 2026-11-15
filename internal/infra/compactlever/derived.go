package compactlever

import (
	"time"

	"go.klarlabs.de/tokenops/internal/capability/avoidable"
	coachcap "go.klarlabs.de/tokenops/internal/capability/coach"
	"go.klarlabs.de/tokenops/internal/capability/findings"
	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/contexts/spend/spend"
)

// derivedFloorTokens is the smallest context line worth setting. A band
// edge below it comes from short sessions, not from where instructions
// start to go worse.
const derivedFloorTokens = 20_000

// planFor is the compaction lines to set: the developer's own derived
// context line when a current snapshot measured one, else the configured
// plan value. Both agents compact at the same measured line, since it is
// a property of the developer's instructions, not of the client.
func planFor(cfg config.Config, snapshotDir string, now time.Time) Plan {
	at := func(prefix string) int64 {
		return coachcap.CompactAt(cfg, prefix)
	}
	p := Plan{ClaudeCompactAt: at("claude-code:"), CodexCompactAt: at("codex:"), OpencodeShare: OpencodeShare, Window: spend.ContextWindow}
	if line := derivedLine(snapshotDir, now); line > 0 {
		// Gemini CLI and Pi have no configured plan value: they are set
		// only from the measured line.
		p.ClaudeCompactAt, p.CodexCompactAt, p.GeminiCompactAt, p.PiCompactAt = line, line, line, line
	}
	return p
}

// derivedLine is the context line the last avoidable snapshot measured,
// 0 when there is none, it is stale, or it is below the floor.
func derivedLine(dir string, now time.Time) int64 {
	s, err := avoidable.ReadSnapshot(dir)
	if err != nil || s == nil || now.Sub(s.ComputedAt) > avoidable.MaxSnapshotAge {
		return 0
	}
	cl := s.Report.ContextLine
	if cl == nil || cl.Tokens < derivedFloorTokens {
		return 0
	}
	return cl.Tokens
}

// snapshotDir is where the daemon keeps the avoidable snapshot.
func snapshotDir() string { return findings.DefaultDir() }
