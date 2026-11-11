package prompts

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A caller that reads a transcript itself gets, line by line, what
// Extract gets from the file.
func TestLineParserReadsWhatExtractReads(t *testing.T) {
	lines := map[Source][]string{
		SourceClaudeCode: {
			`{"type":"user","timestamp":"2026-05-16T10:00:00Z","sessionId":"s","message":{"content":"plain"}}`,
			`{"type":"user","timestamp":"2026-05-16T10:00:01Z","sessionId":"s","message":{"content":[{"type":"text","text":"in parts"}]}}`,
			`{"type":"assistant","timestamp":"2026-05-16T10:00:02Z","sessionId":"s","message":{"content":[{"type":"text","text":"the user asked"}]}}`,
			`{"type":"user","timestamp":"2026-05-16T10:00:03Z","sessionId":"s","message":{"content":[{"type":"tool_result","tool_use_id":"t","content":"out"}]}}`,
			`{"type":"user","timestamp":"2026-05-16T10:00:04Z","sessionId":"s","message":{"content":"<system-reminder>x</system-reminder>"}}`,
			`{"type":"summary","summary":"nothing typed"}`,
			`not json, though it says "user"`,
			``,
		},
		SourceCodex: {
			`{"role":"user","content":[{"type":"input_text","text":"named by the file"}]}`,
			`{"role":"user","timestamp":"2026-05-17T09:00:00Z","content":[{"type":"input_text","text":"stamped"}]}`,
			`{"role":"assistant","content":[{"type":"output_text","text":"done"}]}`,
		},
	}
	for src, ls := range lines {
		dir := t.TempDir()
		path := writeJSONL(t, dir, "rollout-2026-05-16T10-00-00-x.jsonl", ls...)
		want, err := Extract(ExtractOptions{Root: dir, Source: src})
		if err != nil {
			t.Fatal(err)
		}
		if len(want) < 2 {
			t.Fatalf("%s: the fixture holds %d prompts, want some", src, len(want))
		}
		parse := LineParser(path, src)
		var got []UserPrompt
		for _, l := range ls {
			if p, ok := parse([]byte(l)); ok {
				got = append(got, p)
			}
		}
		if len(got) != len(want) {
			t.Fatalf("%s: parsed %d prompts, Extract read %d", src, len(got), len(want))
		}
		for i := range want {
			if got[i].Text != want[i].Text || got[i].SessionID != want[i].SessionID || !got[i].Timestamp.Equal(want[i].Timestamp) {
				t.Errorf("%s prompt %d = %+v, Extract read %+v", src, i, got[i], want[i])
			}
		}
	}
}

// A Codex line with no time of its own takes the session's, which the
// rollout file is named after.
func TestLineParserFallsBackToTheCodexFileName(t *testing.T) {
	parse := LineParser(filepath.Join("x", "rollout-2026-05-16T10-00-00-abc.jsonl"), SourceCodex)
	p, ok := parse([]byte(`{"role":"user","content":[{"type":"input_text","text":"hi"}]}`))
	if !ok {
		t.Fatal("no prompt")
	}
	if p.SessionID != "rollout-2026-05-16T10-00-00-abc" {
		t.Errorf("session %q, want the file's stem", p.SessionID)
	}
	if want := time.Date(2026, 5, 16, 10, 0, 0, 0, time.UTC); !p.Timestamp.Equal(want) {
		t.Errorf("at %s, want %s from the file name", p.Timestamp, want)
	}
}

// The check that spares decoding a line must not lose a prompt: the word
// it looks for is the value both harnesses mark a user's line with,
// however the line is spaced.
func TestLineParserKeepsSpacedLines(t *testing.T) {
	parse := LineParser("s.jsonl", SourceClaudeCode)
	if _, ok := parse([]byte(`{ "type" : "user" , "timestamp" : "2026-05-16T10:00:00Z", "sessionId": "s", "message": { "content": "spaced" } }`)); !ok {
		t.Error("a spaced Claude Code line is not read")
	}
	parse = LineParser("rollout-2026-05-16T10-00-00-x.jsonl", SourceCodex)
	if _, ok := parse([]byte(`{ "role" : "user", "content": [ { "type": "input_text", "text": "spaced" } ] }`)); !ok {
		t.Error("a spaced Codex line is not read")
	}
}

func TestJSONLRootsNamesTheHarnessOfAGivenRoot(t *testing.T) {
	dir := t.TempDir()
	codex := filepath.Join(dir, ".codex", "sessions")
	for root, want := range map[string]Source{dir: SourceClaudeCode, codex: SourceCodex} {
		got, err := JSONLRoots(root)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 1 || got[0].Dir != root || got[0].Source != want {
			t.Errorf("JSONLRoots(%q) = %+v, want that root as %s", root, got, want)
		}
	}
}

// With no root, the trees are the harnesses' own that exist: one that is
// not installed is not walked, and is no error.
func TestJSONLRootsFindsTheDefaultTreesThatExist(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("CODEX_HOME", "")

	got, err := JSONLRoots("")
	if err != nil || len(got) != 0 {
		t.Fatalf("JSONLRoots with neither harness = %+v, %v; want none", got, err)
	}
	codex := filepath.Join(home, ".codex", "sessions")
	if err := os.MkdirAll(codex, 0o755); err != nil {
		t.Fatal(err)
	}
	got, err = JSONLRoots("")
	if err != nil || len(got) != 1 || got[0].Dir != codex || got[0].Source != SourceCodex {
		t.Fatalf("JSONLRoots with Codex alone = %+v, %v", got, err)
	}
	claude := filepath.Join(home, ".claude", "projects")
	if err := os.MkdirAll(claude, 0o755); err != nil {
		t.Fatal(err)
	}
	got, err = JSONLRoots("")
	if err != nil || len(got) != 2 || got[0].Dir != claude || got[0].Source != SourceClaudeCode || got[1].Dir != codex {
		t.Fatalf("JSONLRoots with both = %+v, %v", got, err)
	}
}

// The two words Analyze counts by, for a caller that keeps the verdict
// and not the text.
func TestIsAcknowledgementAndIsRegenerateAreWhatAnalyzeCounts(t *testing.T) {
	texts := []string{
		"ok", " OK ", "Yes please", "go ahead", "keep going", "continue",
		"that's wrong", "Try again", "nope", "no, not what I wanted", "redo it",
		"refactor the retry path", "okay then do the other one", "", "wrongly named, rename it",
	}
	var acks, regenerates int
	for _, text := range texts {
		f := Analyze([]UserPrompt{{Text: text}})
		if got := IsAcknowledgement(text); got != (f.Acknowledgements == 1) {
			t.Errorf("IsAcknowledgement(%q) = %v, Analyze counted %d", text, got, f.Acknowledgements)
		}
		if got := IsRegenerate(text); got != (f.Regenerates == 1) {
			t.Errorf("IsRegenerate(%q) = %v, Analyze counted %d", text, got, f.Regenerates)
		}
		if IsAcknowledgement(text) {
			acks++
		}
		if IsRegenerate(text) {
			regenerates++
		}
	}
	if acks != 6 || regenerates != 5 {
		t.Errorf("%d acknowledgements and %d rejections in %q, want 6 and 5", acks, regenerates, strings.Join(texts, " | "))
	}
}
