package avoidable

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/sessions"
)

// A transcript line written at a time close to now, in the shape the Claude
// Code client writes: an instruction, a model turn with its usage, and a
// tool result.
func claudeLines(at time.Time, session string) string {
	ts := func(d time.Duration) string { return at.Add(d).UTC().Format(time.RFC3339Nano) }
	return `{"type":"user","timestamp":"` + ts(0) + `","sessionId":"` + session + `","message":{"content":"task"}}` + "\n" +
		`{"type":"assistant","timestamp":"` + ts(time.Second) + `","sessionId":"` + session + `","message":{"model":"claude-opus-5-5","usage":{"input_tokens":120,"cache_read_input_tokens":900},"content":[{"type":"text","text":"ok"}]}}` + "\n"
}

// A report with nothing changed since the last opens no transcript: the
// shared cache answers from what it holds, so the daemon's periodic reports
// do not read the whole window again.
func TestSecondReportWithNothingChangedOpensNoTranscript(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	root := t.TempDir()
	now := time.Now()
	dir := filepath.Join(root, "proj-a")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "s1.jsonl"), []byte(claudeLines(now.Add(-time.Hour), "s1")), 0o600); err != nil {
		t.Fatal(err)
	}

	cache := sessions.NewTranscriptCache()
	read := transcriptRecords(root, cache)
	first, err := read(context.Background(), now.Add(-24*time.Hour), now)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) == 0 {
		t.Fatal("the first report read no records from the fixture")
	}
	read1 := cache.BytesRead()
	if read1 == 0 {
		t.Fatal("the first report read no bytes")
	}

	second, err := read(context.Background(), now.Add(-24*time.Hour), now)
	if err != nil {
		t.Fatal(err)
	}
	if got := cache.BytesRead(); got != read1 {
		t.Errorf("second report read %d more transcript bytes; want none", got-read1)
	}
	if len(second) != len(first) {
		t.Errorf("second report has %d records, first had %d", len(second), len(first))
	}
}
