package agentdx

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/klauspost/compress/zstd"
)

// A cold Codex rollout is only its .jsonl.zst copy; its turns must still
// be read, not skipped.
func TestExtractCodexReadsACompressedRollout(t *testing.T) {
	root := t.TempDir()
	day := filepath.Join(root, "2026", "01", "02")
	if err := os.MkdirAll(day, 0o750); err != nil {
		t.Fatal(err)
	}
	body := `{"timestamp":"2026-01-02T10:00:00Z","type":"session_meta","payload":{"id":"packed-2"}}
{"timestamp":"2026-01-02T10:00:01Z","type":"event_msg","payload":{"type":"user_message"}}
`
	var buf bytes.Buffer
	enc, err := zstd.NewWriter(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := enc.Write([]byte(body)); err != nil {
		t.Fatal(err)
	}
	if err := enc.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(day, "rollout-2026-01-02T10-00-00-packed-2.jsonl.zst"), buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	recs, err := ExtractCodex(ExtractOptions{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) == 0 {
		t.Fatal("the compressed rollout's prompt was skipped")
	}
	for _, r := range recs {
		// The id is the file name after "rollout-", as for a plain rollout.
		if want := "2026-01-02T10-00-00-packed-2"; r.SessionID != want {
			t.Errorf("session = %q, want %q", r.SessionID, want)
		}
	}
}
