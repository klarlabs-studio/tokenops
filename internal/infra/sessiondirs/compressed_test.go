package sessiondirs

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/klauspost/compress/zstd"
)

// A cold Codex rollout is published as .jsonl.zst; its session still
// maps to its directory.
func TestCodexDirReadsACompressedRollout(t *testing.T) {
	isolateHome(t)
	var buf bytes.Buffer
	enc, err := zstd.NewWriter(&buf)
	if err != nil {
		t.Fatal(err)
	}
	body := lines(`{"type":"session_meta","payload":{"id":"packed-1","cwd":"/w/packed","git":{"branch":"main"}}}`, `{"type":"event_msg"}`)
	if _, err := enc.Write([]byte(body)); err != nil {
		t.Fatal(err)
	}
	if err := enc.Close(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "rollout-2026-01-02T00-00-00-packed-1.jsonl.zst")
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	id, d, ok := codexDir(path)
	if !ok || id != "packed-1" || d != (Dir{CWD: "/w/packed", Branch: "main"}) {
		t.Errorf("codexDir = %q, %+v, %v; want packed-1 in /w/packed on main", id, d, ok)
	}
}
