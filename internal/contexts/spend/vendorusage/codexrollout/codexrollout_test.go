package codexrollout

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/klauspost/compress/zstd"
)

func TestIsRolloutName(t *testing.T) {
	for name, want := range map[string]bool{
		"rollout-a.jsonl":        true,
		"rollout-a.jsonl.zst":    true,
		"rollout-compress-1.tmp": false,
		"notes.jsonl.zst.bak":    false,
		"rollout-a.json":         false,
	} {
		if got := IsRolloutName(name); got != want {
			t.Errorf("IsRolloutName(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestOpenDecompressesACompressedRollout(t *testing.T) {
	var buf bytes.Buffer
	enc, err := zstd.NewWriter(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := enc.Write([]byte("line one\nline two\n")); err != nil {
		t.Fatal(err)
	}
	if err := enc.Close(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "rollout-x.jsonl.zst")
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	got, err := io.ReadAll(f)
	if err != nil || string(got) != "line one\nline two\n" {
		t.Errorf("read %q, %v", got, err)
	}
}

// A corrupt compressed rollout must fail when read, not read as empty.
func TestOpenCorruptCompressedRolloutFailsOnRead(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout-x.jsonl.zst")
	if err := os.WriteFile(path, []byte("not zstd, not a frame"), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := Open(path)
	if err != nil {
		return // refusing at open is also a failure, not an empty success
	}
	defer func() { _ = f.Close() }()
	if _, err := io.ReadAll(f); err == nil {
		t.Error("a corrupt compressed rollout read without error")
	}
}

func TestSessionID(t *testing.T) {
	for name, want := range map[string]string{
		"/c/sessions/2026/01/02/rollout-2026-01-02T10-00-00-abc.jsonl":     "2026-01-02T10-00-00-abc",
		"/c/sessions/2026/01/02/rollout-2026-01-02T10-00-00-abc.jsonl.zst": "2026-01-02T10-00-00-abc",
	} {
		got, ok := SessionID(name)
		if !ok || got != want {
			t.Errorf("SessionID(%q) = %q, %v; want %q", name, got, ok, want)
		}
	}
}
