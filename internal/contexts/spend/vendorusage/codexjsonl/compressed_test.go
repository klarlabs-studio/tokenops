package codexjsonl

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/klauspost/compress/zstd"
)

// compressRollout writes content as a zstd-compressed rollout, the way
// Codex's cold-rollout pass does (codex-rs/rollout/src/compression.rs:
// zstd level 3, published as rollout-<…>.jsonl.zst).
func compressRollout(t *testing.T, path, content string) {
	t.Helper()
	var buf bytes.Buffer
	enc, err := zstd.NewWriter(&buf, zstd.WithEncoderLevel(zstd.SpeedDefault))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := enc.Write([]byte(content)); err != nil {
		t.Fatal(err)
	}
	if err := enc.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

// twoTurns is a session with two token_count turns, each carrying usage.
const twoTurns = `{"timestamp":"2026-04-17T08:44:43.195Z","type":"session_meta","payload":{"id":"zst-session","model_provider":"openai"}}
{"timestamp":"2026-04-17T08:44:50.000Z","type":"turn_context","payload":{"model":"gpt-5.3-codex"}}
{"timestamp":"2026-04-17T08:45:14.725Z","type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":100,"cached_input_tokens":50,"output_tokens":20,"reasoning_output_tokens":5,"total_tokens":125},"model_context_window":258400},"rate_limits":{"primary":{"used_percent":10.0,"window_minutes":300,"resets_at":1776431887},"secondary":{"used_percent":2.0,"window_minutes":10080,"resets_at":1777018687},"plan_type":"plus"}}}
{"timestamp":"2026-04-17T08:46:14.725Z","type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":200,"cached_input_tokens":0,"output_tokens":30,"reasoning_output_tokens":0,"total_tokens":230},"model_context_window":258400},"rate_limits":{"primary":{"used_percent":11.0,"window_minutes":300,"resets_at":1776431887},"secondary":{"used_percent":2.0,"window_minutes":10080,"resets_at":1777018687},"plan_type":"plus"}}}
`

func TestFindSessionFilesIncludesCompressedRollouts(t *testing.T) {
	root := t.TempDir()
	day := filepath.Join(root, "2026", "01", "02")
	if err := os.MkdirAll(day, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"rollout-a.jsonl", "rollout-b.jsonl.zst", "rollout-compress-1.tmp", "notes.zst"} {
		if err := os.WriteFile(filepath.Join(day, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	files, err := FindSessionFiles(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 {
		t.Fatalf("found %v, want the .jsonl and the .jsonl.zst rollouts only", files)
	}
}

func TestReadFileReadsACompressedRolloutLikeThePlainOne(t *testing.T) {
	dir := t.TempDir()
	plain := filepath.Join(dir, "rollout-plain.jsonl")
	packed := filepath.Join(dir, "rollout-packed.jsonl.zst")
	if err := os.WriteFile(plain, []byte(twoTurns), 0o644); err != nil {
		t.Fatal(err)
	}
	compressRollout(t, packed, twoTurns)

	want := collectTurns(t, plain)
	got := collectTurns(t, packed)
	if len(want) != 2 {
		t.Fatalf("plain rollout yielded %d turns, want 2", len(want))
	}
	if len(got) != len(want) {
		t.Fatalf("compressed rollout yielded %d turns, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i].SessionID != want[i].SessionID || got[i].RecordSequence != want[i].RecordSequence ||
			got[i].InputTokens != want[i].InputTokens || got[i].Model != want[i].Model ||
			got[i].RateLimits != want[i].RateLimits {
			t.Errorf("turn %d differs: compressed %+v, plain %+v", i, got[i], want[i])
		}
	}
}

func TestReadFileReportsACorruptCompressedRollout(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout-bad.jsonl.zst")
	if err := os.WriteFile(path, []byte("this is not zstd at all"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ReadFile(path, func(Turn) error { return nil }); err == nil {
		t.Fatal("a corrupt compressed rollout read without error; it must surface, not read as empty")
	}
}

// A plain rollout and its compressed copy exist together for a moment
// while Codex publishes the .zst before removing the source. Each turn
// must still be published once.
func TestPollerPublishesACompressedRolloutOnceAcrossScans(t *testing.T) {
	root := t.TempDir()
	day := filepath.Join(root, "2026", "01", "02")
	if err := os.MkdirAll(day, 0o755); err != nil {
		t.Fatal(err)
	}
	compressRollout(t, filepath.Join(day, "rollout-packed.jsonl.zst"), twoTurns)
	bus := &captureBus{}
	p := NewPoller(bus, PollerOptions{Root: root, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	p.scan(context.Background(), root)
	p.scan(context.Background(), root)
	if got := bus.PublishedCount(); got != 2 {
		t.Fatalf("published %d turns over two scans, want 2", got)
	}

	if err := os.WriteFile(filepath.Join(day, "rollout-packed.jsonl"), []byte(twoTurns), 0o644); err != nil {
		t.Fatal(err)
	}
	p.scan(context.Background(), root)
	if got := bus.PublishedCount(); got != 2 {
		t.Errorf("after the plain copy appeared, published %d turns, want still 2 (no duplicates)", got)
	}
}

func collectTurns(t *testing.T, path string) []Turn {
	t.Helper()
	var out []Turn
	if err := ReadFile(path, func(turn Turn) error {
		out = append(out, turn)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return out
}
