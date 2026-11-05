package claudecodejsonl

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
)

// A poller started after another one's checkpoint, as a restarted daemon's
// is, reads only what the transcripts gained since.
func TestPollerResumesFromItsCheckpoint(t *testing.T) {
	root := t.TempDir()
	proj := filepath.Join(root, "proj")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(proj, "sess.jsonl")
	if err := os.WriteFile(path, []byte(sampleTurn+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	opts := PollerOptions{Root: root, Logger: logger, CheckpointPath: filepath.Join(t.TempDir(), "claude-tail.json"), checkpointLag: -1}
	first := NewPoller(&captureBus{}, opts)
	first.restore()
	first.scan(context.Background(), root)
	first.checkpoint()

	fh, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fh.WriteString(sampleTurnTwo + "\n"); err != nil {
		t.Fatal(err)
	}
	_ = fh.Close()

	bus := &captureBus{}
	restarted := NewPoller(bus, opts)
	restarted.restore()
	restarted.scan(context.Background(), root)
	if want := int64(len(sampleTurnTwo) + 1); restarted.tail.BytesParsed != want {
		t.Fatalf("parsed %d bytes after a restart, want only the %d appended", restarted.tail.BytesParsed, want)
	}
	if got := bus.PublishedCount(); got != 1 {
		t.Fatalf("published %d turns after a restart, want the one appended", got)
	}
}
