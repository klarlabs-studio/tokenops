package mcp

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.klarlabs.de/tokenops/internal/storage/sqlite"
)

// TestVerifyToolCharacterization pins tokenops_verify over fixed
// transcripts, so moving its transcript read onto capability/sessions
// cannot change what an agent reads.
func TestVerifyToolCharacterization(t *testing.T) {
	root := t.TempDir()
	for rel, lines := range map[string][]string{
		"proj-a/s1.jsonl": {
			`{"type":"user","timestamp":"2026-01-05T10:00:00Z","sessionId":"s1","cwd":"/work/app","message":{"content":"fix the retry path"}}`,
			`{"type":"assistant","timestamp":"2026-01-05T10:01:00Z","sessionId":"s1","message":{"model":"claude-sonnet-4-5","usage":{"input_tokens":15000,"output_tokens":600},"content":[{"type":"tool_use","name":"Edit","input":{"file_path":"/work/app/upload.go"}}]}}`,
			`{"type":"user","timestamp":"2026-01-05T11:00:00Z","sessionId":"s1","cwd":"/work/app","message":{"content":"now add a test for it"}}`,
			`{"type":"assistant","timestamp":"2026-01-05T11:02:00Z","sessionId":"s1","message":{"model":"claude-sonnet-4-5","usage":{"input_tokens":21000,"output_tokens":300},"content":[{"type":"text","text":"done"}]}}`,
		},
	} {
		full := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	store, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "events.db"), sqlite.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	srv := NewServer("tokenops", "test", slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := RegisterVerifyTool(srv, VerifyDeps{Store: store, Root: root}); err != nil {
		t.Fatal(err)
	}
	assertGolden(t, "verify/all.json", roundFloats(execTool(t, srv, "tokenops_verify", map[string]any{"days": -1})))
}
