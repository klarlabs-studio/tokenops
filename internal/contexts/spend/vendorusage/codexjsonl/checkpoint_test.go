package codexjsonl

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

// A poller started after another one's checkpoint, as a restarted daemon's
// is, reads only what the rollouts gained since, gives the turns it finds
// the IDs one uninterrupted read gives them, and does not read an
// unchanged compressed rollout again.
func TestPollerResumesFromItsCheckpoint(t *testing.T) {
	root := t.TempDir()
	day := filepath.Join(root, "2026", "11", "05")
	if err := os.MkdirAll(day, 0o755); err != nil {
		t.Fatal(err)
	}
	live := filepath.Join(day, "rollout-live.jsonl")
	turn := func(ts string, in int) string {
		return `{"timestamp":"` + ts + `","type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":` + strconv.Itoa(in) + `,"cached_input_tokens":0,"output_tokens":10,"reasoning_output_tokens":0,"total_tokens":110}}}}` + "\n"
	}
	head := `{"timestamp":"2026-11-05T10:00:00.000Z","type":"session_meta","payload":{"id":"sess-r"}}` + "\n" +
		`{"timestamp":"2026-11-05T10:00:01.000Z","type":"turn_context","payload":{"model":"gpt-6-luna"}}` + "\n" +
		turn("2026-11-05T10:00:02.000Z", 100)
	if err := os.WriteFile(live, []byte(head), 0o644); err != nil {
		t.Fatal(err)
	}
	compressRollout(t, filepath.Join(day, "rollout-old.jsonl.zst"), codexFile)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	checkpoint := filepath.Join(t.TempDir(), "codex-tail.json")
	opts := PollerOptions{Root: root, Logger: logger, CheckpointPath: checkpoint, checkpointLag: -1}
	first := NewPoller(&captureBus{}, opts)
	first.restore()
	first.scan(context.Background(), root)
	first.checkpoint()

	appended := turn("2026-11-05T10:05:00.000Z", 200)
	fh, err := os.OpenFile(live, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fh.WriteString(appended); err != nil {
		t.Fatal(err)
	}
	_ = fh.Close()

	bus := &captureBus{}
	restarted := NewPoller(bus, opts)
	restarted.restore()
	restarted.scan(context.Background(), root)
	if restarted.tail.BytesParsed != int64(len(appended)) {
		t.Fatalf("parsed %d bytes after a restart, want only the %d appended", restarted.tail.BytesParsed, len(appended))
	}
	if got := bus.PublishedCount(); got != 1 {
		t.Fatalf("published %d turns after a restart, want the one appended (the compressed rollout was read again?)", got)
	}

	whole := &captureBus{}
	NewPoller(whole, PollerOptions{Root: root, Logger: logger}).scan(context.Background(), root)
	var want string
	for _, e := range whole.envelopes {
		if e.Attributes["session_id"] == "sess-r" && e.Timestamp.Minute() == 5 {
			want = e.ID
		}
	}
	if got := bus.envelopes[0]; got.ID != want || got.Attributes["session_id"] != "sess-r" {
		t.Fatalf("resumed turn %s (session %q), want %s as one read gives it", got.ID, got.Attributes["session_id"], want)
	}
}
