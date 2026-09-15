package agentdx

import (
	"os"
	"path/filepath"
	"testing"
)

// seedProject writes one transcript under a named project directory.
func seedProject(t *testing.T, root, project string, lines ...string) {
	t.Helper()
	dir := filepath.Join(root, project)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	body := ""
	for _, l := range lines {
		body += l + "\n"
	}
	if err := os.WriteFile(filepath.Join(dir, "s.jsonl"), []byte(body), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
}

const (
	realProject    = "-Users-me-src-api"
	scratchProject = "-private-var-folders-wz-abc-T-worksim-claudecode-1"
)

func promptLine(session, text string) string {
	return `{"type":"user","timestamp":"2099-01-01T10:00:00Z","sessionId":"` + session +
		`","message":{"content":"` + text + `"}}`
}

// The defect this fixes: a simulation harness produced 94% of a real
// 7-day window, and dx graded it. Median turns per instruction read 2.0
// against the operator's actual 20.0, and every dimension came back A.
func TestExtractSkipsScratchProjects(t *testing.T) {
	root := t.TempDir()
	seedProject(t, root, realProject, promptLine("real", "rewrite the boundary heuristic"))
	seedProject(t, root, scratchProject, promptLine("sim", "INTENT"))

	recs, err := Extract(ExtractOptions{Root: root, WithPromptText: true})
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if len(recs) != 1 {
		t.Fatalf("got %d records, want 1 — the harness session should be out of scope", len(recs))
	}
	if recs[0].SessionID != "real" {
		t.Errorf("SessionID = %q, want the operator's own session", recs[0].SessionID)
	}
}

// A default that cannot be turned off is its own kind of dishonesty:
// someone eventually wants to measure the harness.
func TestExtractIncludesScratchWhenAsked(t *testing.T) {
	root := t.TempDir()
	seedProject(t, root, realProject, promptLine("real", "rewrite the boundary heuristic"))
	seedProject(t, root, scratchProject, promptLine("sim", "INTENT"))

	recs, err := Extract(ExtractOptions{Root: root, IncludeScratch: true})
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if len(recs) != 2 {
		t.Fatalf("got %d records with --include-scratch, want both", len(recs))
	}
}

// ~/.claude/projects is under the system temp directory in every test
// that builds a fixture. The rule reads the project name, never the
// machine path, so a whole test suite is not silently emptied.
func TestExtractDoesNotMistakeAFixtureRootForScratch(t *testing.T) {
	root := t.TempDir() // itself under /var/folders on macOS
	seedProject(t, root, "proj", promptLine("s", "do the thing"))

	recs, err := Extract(ExtractOptions{Root: root})
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if len(recs) == 0 {
		t.Fatal("a fixture under the test temp dir was treated as scratch work")
	}
}
