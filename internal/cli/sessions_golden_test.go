package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/storage/sqlite"
)

// sessionsFixture is two Claude Code sessions at fixed instants: one
// instruction that drags (many turns, a file edited again after moving
// on, an interrupt) and one that lands first time, so every dx dimension,
// every story section and the verify reconstruction have something to
// say.
var sessionsFixture = map[string][]string{
	"proj-a/s1.jsonl": {
		`{"type":"user","timestamp":"2026-01-05T10:00:00Z","sessionId":"s1","cwd":"/work/app","message":{"content":"fix the retry path in the uploader"}}`,
		`{"type":"assistant","timestamp":"2026-01-05T10:00:20Z","sessionId":"s1","message":{"model":"claude-sonnet-4-5","usage":{"input_tokens":12000,"output_tokens":400},"content":[{"type":"tool_use","name":"Read","input":{"file_path":"/work/app/upload.go"}}]}}`,
		`{"type":"assistant","timestamp":"2026-01-05T10:01:00Z","sessionId":"s1","message":{"model":"claude-sonnet-4-5","usage":{"input_tokens":15000,"output_tokens":600},"content":[{"type":"tool_use","name":"Edit","input":{"file_path":"/work/app/upload.go"}}]}}`,
		`{"type":"assistant","timestamp":"2026-01-05T10:02:00Z","sessionId":"s1","message":{"model":"claude-sonnet-4-5","usage":{"input_tokens":19000,"output_tokens":500},"content":[{"type":"tool_use","name":"Edit","input":{"file_path":"/work/app/retry.go"}}]}}`,
		`{"type":"assistant","timestamp":"2026-01-05T10:03:00Z","sessionId":"s1","message":{"model":"claude-sonnet-4-5","usage":{"input_tokens":24000,"output_tokens":700},"content":[{"type":"tool_use","name":"Edit","input":{"file_path":"/work/app/upload.go"}}]}}`,
		`{"type":"user","timestamp":"2026-01-05T10:03:30Z","sessionId":"s1","cwd":"/work/app","message":{"content":"[Request interrupted by user]"}}`,
		`{"type":"user","timestamp":"2026-01-05T10:04:00Z","sessionId":"s1","cwd":"/work/app","message":{"content":"no, that's wrong — keep the backoff"}}`,
		`{"type":"assistant","timestamp":"2026-01-05T10:05:00Z","sessionId":"s1","message":{"model":"claude-sonnet-4-5","usage":{"input_tokens":26000,"output_tokens":300},"content":[{"type":"tool_use","name":"Edit","input":{"file_path":"/work/app/retry.go"}}]}}`,
		`{"type":"assistant","timestamp":"2026-01-05T10:06:00Z","sessionId":"s1","message":{"model":"claude-sonnet-4-5","usage":{"input_tokens":27000,"output_tokens":200},"content":[{"type":"text","text":"done"}]}}`,
	},
	"proj-b/s2.jsonl": {
		`{"type":"user","timestamp":"2026-01-06T09:00:00Z","sessionId":"s2","cwd":"/work/lib","message":{"content":"add a changelog entry for 1.4"}}`,
		`{"type":"assistant","timestamp":"2026-01-06T09:00:30Z","sessionId":"s2","message":{"model":"claude-haiku-4-5","usage":{"input_tokens":4000,"output_tokens":200},"content":[{"type":"tool_use","name":"Edit","input":{"file_path":"/work/lib/CHANGELOG.md"}}]}}`,
		`{"type":"assistant","timestamp":"2026-01-06T09:01:00Z","sessionId":"s2","message":{"model":"claude-haiku-4-5","usage":{"input_tokens":4500,"output_tokens":100},"content":[{"type":"text","text":"added"}]}}`,
	},
}

// writeSessionsFixture writes the fixture under root and returns root.
func writeSessionsFixture(t *testing.T, root string) string {
	t.Helper()
	for rel, lines := range sessionsFixture {
		full := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// TestSessionsOutputCharacterization pins `tokenops dx`, `tokenops story`
// and `tokenops verify` over fixed transcripts byte for byte, so moving
// their transcript reads onto capability/sessions cannot change them.
func TestSessionsOutputCharacterization(t *testing.T) {
	// verify has no --root: it reads every client's default location
	// under HOME, so HOME holds the fixture and nothing else.
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	root := writeSessionsFixture(t, filepath.Join(home, ".claude", "projects"))
	db := filepath.Join(t.TempDir(), "events.db")
	store, err := sqlite.Open(context.Background(), db, sqlite.Options{})
	if err != nil {
		t.Fatal(err)
	}
	_ = store.Close()
	// story renders local clock times; CI runs in UTC.
	local := time.Local
	time.Local = time.UTC
	t.Cleanup(func() { time.Local = local })

	pinned := []string{"--root", root, "--source", "claude-code", "--days", "0"}
	cases := []struct {
		golden string
		args   []string
	}{
		{"sessions/dx.txt", append([]string{"dx", "--fresh"}, pinned...)},
		{"sessions/dx.json", append([]string{"dx", "--fresh", "--json"}, pinned...)},
		{"sessions/story.txt", append([]string{"story"}, pinned...)},
		{"sessions/story.json", append([]string{"story", "--json", "--limit", "0"}, pinned...)},
		{"sessions/story_handoff.txt", append([]string{"story", "--for", "handoff", "--idle-gap", "30s"}, pinned...)},
		{"sessions/verify.txt", []string{"verify", "--days", "0", "--db", db, "--each"}},
		{"sessions/verify.json", []string{"verify", "--days", "0", "--db", db, "--json"}},
	}
	for _, tc := range cases {
		t.Run(tc.golden, func(t *testing.T) {
			out, err := executeRoot(t, tc.args...)
			if err != nil {
				t.Fatalf("%v: %v", tc.args, err)
			}
			out = strings.ReplaceAll(out, home, "<HOME>")
			if strings.HasSuffix(tc.golden, ".json") {
				out = roundFloats(out)
			}
			assertGolden(t, tc.golden, out)
		})
	}
}
