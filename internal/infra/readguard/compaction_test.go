package readguard

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func appendLine(t *testing.T, path, line string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if _, err := f.WriteString(line + "\n"); err != nil {
		t.Fatal(err)
	}
}

// A compaction drops the files the agent read from its context, so a read
// before the compaction no longer makes a re-read redundant. Reads after it
// still do.
func TestForgetCompactedAllowsTheReReadAfterACompaction(t *testing.T) {
	dir := t.TempDir()
	f := tmpFile(t, "package main\n")
	transcript := filepath.Join(t.TempDir(), "s1.jsonl")
	appendLine(t, transcript, `{"type":"user","timestamp":"2026-11-01T10:00:00Z","message":{"content":"go"}}`)
	t0 := time.Date(2026, 11, 1, 10, 0, 1, 0, time.UTC)

	ForgetCompacted(dir, "s1", transcript)
	Evaluate(dir, "s1", "", f, false, ModeActive, t0)
	ForgetCompacted(dir, "s1", transcript)
	if d := Evaluate(dir, "s1", "", f, false, ModeActive, t0.Add(time.Second)); !d.Block {
		t.Fatalf("before any compaction the re-read is refused: %+v", d)
	}

	appendLine(t, transcript, `{"type":"system","subtype":"compact_boundary","timestamp":"2026-11-01T10:05:00Z","content":"Conversation compacted"}`)
	ForgetCompacted(dir, "s1", transcript)
	if d := Evaluate(dir, "s1", "", f, false, ModeActive, t0.Add(5*time.Minute)); d.Block {
		t.Fatalf("a read from before the compaction refused the one after: %+v", d)
	}
	ForgetCompacted(dir, "s1", transcript)
	if d := Evaluate(dir, "s1", "", f, false, ModeActive, t0.Add(6*time.Minute)); !d.Block {
		t.Fatalf("a re-read after the compaction's own read is allowed: %+v", d)
	}
}

// State written before an upgrade has no read times; a compaction in a
// transcript read for the first time forgets those reads too.
func TestForgetCompactedForgetsReadsOfUnknownTime(t *testing.T) {
	dir := t.TempDir()
	f := tmpFile(t, "package main\n")
	st := sessionState{Paths: map[string]pathState{f: {FP: mustFingerprint(t, f), Reads: 1}}}
	saveSession(dir, "s1", st)
	transcript := filepath.Join(t.TempDir(), "s1.jsonl")
	appendLine(t, transcript, `{"type":"system","subtype":"compact_boundary","timestamp":"2026-11-01T10:05:00Z"}`)

	ForgetCompacted(dir, "s1", transcript)
	if d := Evaluate(dir, "s1", "", f, false, ModeActive, time.Date(2026, 11, 1, 10, 6, 0, 0, time.UTC)); d.Block {
		t.Fatalf("refused on a read of unknown time before a compaction: %+v", d)
	}
}

func mustFingerprint(t *testing.T, path string) fingerprint {
	t.Helper()
	fp, ok := statFingerprint(path)
	if !ok {
		t.Fatal("stat")
	}
	return fp
}
