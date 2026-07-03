package readguard

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func tmpFile(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "main.go")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestEvaluate_RedundantFullReRead(t *testing.T) {
	dir := t.TempDir()
	f := tmpFile(t, "package main\nfunc main(){}\n")
	now := time.Unix(0, 0)

	// First full read: new -> allow.
	d1 := Evaluate(dir, "s1", "", f, false, ModeActive, now)
	if d1.Block || d1.Action != ActionAllow {
		t.Fatalf("first read should allow: %+v", d1)
	}
	// Second identical full read, unchanged, active -> block.
	d2 := Evaluate(dir, "s1", "", f, false, ModeActive, now)
	if !d2.Block || d2.Action != ActionBlocked {
		t.Fatalf("redundant re-read should block in active: %+v", d2)
	}
	if d2.EstTokens <= 0 {
		t.Error("blocked read should estimate reclaimed tokens")
	}
}

func TestEvaluate_ObserveNeverBlocks(t *testing.T) {
	dir := t.TempDir()
	f := tmpFile(t, "content")
	now := time.Unix(0, 0)
	Evaluate(dir, "s1", "", f, false, ModeObserve, now)
	d := Evaluate(dir, "s1", "", f, false, ModeObserve, now)
	if d.Block {
		t.Error("observe mode must never block")
	}
	if d.Action != ActionWouldBlock {
		t.Errorf("redundant read in observe should be would_block, got %s", d.Action)
	}
}

func TestEvaluate_RangedAlwaysAllowed(t *testing.T) {
	dir := t.TempDir()
	f := tmpFile(t, "content")
	now := time.Unix(0, 0)
	Evaluate(dir, "s1", "", f, false, ModeActive, now) // full read recorded
	d := Evaluate(dir, "s1", "", f, true, ModeActive, now)
	if d.Block {
		t.Error("ranged read must never be blocked")
	}
}

func TestEvaluate_ChangedFileAllowed(t *testing.T) {
	dir := t.TempDir()
	f := tmpFile(t, "v1")
	now := time.Unix(0, 0)
	Evaluate(dir, "s1", "", f, false, ModeActive, now)
	// Change the file (size differs -> fingerprint changes).
	if err := os.WriteFile(f, []byte("v2-longer"), 0o644); err != nil {
		t.Fatal(err)
	}
	d := Evaluate(dir, "s1", "", f, false, ModeActive, now)
	if d.Block {
		t.Error("changed file should not be treated as a redundant re-read")
	}
}

func TestEvaluate_SessionIsolation(t *testing.T) {
	dir := t.TempDir()
	f := tmpFile(t, "content")
	now := time.Unix(0, 0)
	Evaluate(dir, "s1", "", f, false, ModeActive, now)
	// A different session has no prior read -> allow.
	d := Evaluate(dir, "s2", "", f, false, ModeActive, now)
	if d.Block {
		t.Error("re-read must be scoped per session")
	}
}

// TestEvaluate_SubagentDoesNotBlockMainAgent reproduces the real friction:
// a subagent reads a file (lands in the subagent's context), then the main
// agent reads the same file in the same session. Because each agent has its
// own context window, the main agent's read must NOT be blocked.
func TestEvaluate_SubagentDoesNotBlockMainAgent(t *testing.T) {
	dir := t.TempDir()
	f := tmpFile(t, "package main\nfunc main(){}\n")
	now := time.Unix(0, 0)

	// Subagent "a1" reads it (into a1's context).
	Evaluate(dir, "s1", "a1", f, false, ModeActive, now)
	// Main agent (empty agent id) reads it — new to the main context.
	d := Evaluate(dir, "s1", "", f, false, ModeActive, now)
	if d.Block {
		t.Fatal("a subagent's read must not block the main agent's read")
	}
	// Two different subagents likewise don't share a ledger.
	d2 := Evaluate(dir, "s1", "a2", f, false, ModeActive, now)
	if d2.Block {
		t.Error("distinct subagents must not share a read ledger")
	}
	// But the SAME agent re-reading unchanged IS still redundant.
	dSame := Evaluate(dir, "s1", "a1", f, false, ModeActive, now)
	if !dSame.Block {
		t.Error("same agent's unchanged full re-read should still block")
	}
}

func TestReadStats_Aggregates(t *testing.T) {
	dir := t.TempDir()
	f := tmpFile(t, "content-here")
	now := time.Unix(0, 0)
	Evaluate(dir, "s1", "", f, false, ModeObserve, now) // allow
	Evaluate(dir, "s1", "", f, false, ModeObserve, now) // would_block
	Evaluate(dir, "s2", "", f, false, ModeActive, now)  // allow (new session)
	Evaluate(dir, "s2", "", f, false, ModeActive, now)  // blocked

	s, err := ReadStats(dir)
	if err != nil {
		t.Fatal(err)
	}
	if s.WouldBlock != 1 || s.Blocked != 1 {
		t.Errorf("want 1 would_block + 1 blocked, got %+v", s)
	}
	if s.ReclaimableTok <= 0 || s.ReclaimedTok <= 0 {
		t.Errorf("token sums should be positive: %+v", s)
	}
	if s.DistinctSessions != 2 {
		t.Errorf("distinct sessions = %d, want 2", s.DistinctSessions)
	}
}

func TestReadStats_RepeatBreakdown(t *testing.T) {
	dir := t.TempDir()
	f := tmpFile(t, "package main\n")
	now := time.Unix(0, 0)

	Evaluate(dir, "s1", "", f, false, ModeObserve, now) // 1st: new
	// post-edit re-read: change the file, then read full again -> allowed, changed.
	if err := os.WriteFile(f, []byte("package main // edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	Evaluate(dir, "s1", "", f, false, ModeObserve, now) // repeat, changed
	// ranged re-read -> allowed, ranged.
	Evaluate(dir, "s1", "", f, true, ModeObserve, now) // repeat, ranged
	// unchanged full re-read -> would_block (reclaimable).
	Evaluate(dir, "s1", "", f, false, ModeObserve, now) // repeat, reclaimable

	s, err := ReadStats(dir)
	if err != nil {
		t.Fatal(err)
	}
	if s.RepeatReads != 3 {
		t.Errorf("repeat reads = %d, want 3", s.RepeatReads)
	}
	if s.RepeatPostEdit != 1 {
		t.Errorf("post-edit = %d, want 1", s.RepeatPostEdit)
	}
	if s.RepeatRanged != 1 {
		t.Errorf("ranged = %d, want 1", s.RepeatRanged)
	}
	if s.WouldBlock != 1 {
		t.Errorf("reclaimable would-block = %d, want 1", s.WouldBlock)
	}
}

func TestParseMode(t *testing.T) {
	if ParseMode("active") != ModeActive || ParseMode("ACTIVE") != ModeActive {
		t.Error("active not parsed")
	}
	if ParseMode("") != ModeObserve || ParseMode("bogus") != ModeObserve {
		t.Error("default should be observe")
	}
}
