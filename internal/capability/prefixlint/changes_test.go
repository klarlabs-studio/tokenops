package prefixlint

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// session builds one Claude Code transcript line by line.
type session struct {
	t     *testing.T
	id    string
	at    time.Time
	n     int
	lines []string
}

func newSession(t *testing.T, id string) *session {
	return &session{t: t, id: id, at: time.Date(2026, 10, 30, 9, 0, 0, 0, time.UTC)}
}

func (s *session) add(v map[string]any) {
	v["sessionId"] = s.id
	v["timestamp"] = s.at.Format(time.RFC3339Nano)
	b, err := json.Marshal(v)
	if err != nil {
		s.t.Fatal(err)
	}
	s.lines = append(s.lines, string(b))
}

// turn is a main-thread reply that read read tokens from the cache and
// wrote write to it, after gap; content blocks are written one line each
// with the same message id, as Claude Code writes them.
func (s *session) turn(gap time.Duration, read, write int, blocks ...map[string]any) {
	s.at = s.at.Add(gap)
	s.n++
	if len(blocks) == 0 {
		blocks = []map[string]any{{"type": "text", "text": "private reply text"}}
	}
	for _, b := range blocks {
		s.add(map[string]any{"type": "assistant", "message": map[string]any{
			"id": fmt.Sprintf("msg_%d", s.n), "model": "claude-opus-5-5", "role": "assistant",
			"content": []map[string]any{b},
			"usage": map[string]any{"input_tokens": 3, "cache_read_input_tokens": read, "cache_creation_input_tokens": write,
				"cache_creation": map[string]any{"ephemeral_1h_input_tokens": write}},
		}})
	}
}

func (s *session) attach(a map[string]any) {
	s.at = s.at.Add(time.Second)
	s.add(map[string]any{"type": "attachment", "attachment": a})
}

func (s *session) sidechainTurn(read, write int) {
	s.add(map[string]any{"type": "assistant", "isSidechain": true, "message": map[string]any{
		"id": "side", "model": "claude-haiku-4-5", "content": []any{},
		"usage": map[string]any{"input_tokens": 3, "cache_read_input_tokens": read, "cache_creation_input_tokens": write},
	}})
}

func (s *session) write(root string) {
	write(s.t, filepath.Join(root, "proj", s.id+".jsonl"), strings.Join(s.lines, "\n")+"\n")
}

func toolsDelta(added, removed []string) map[string]any {
	return map[string]any{"type": "deferred_tools_delta", "addedNames": added, "removedNames": removed}
}

func impactOf(r Report, kind string) (ChangeImpact, bool) {
	for _, c := range r.Changes {
		if c.Kind == kind {
			return c, true
		}
	}
	return ChangeImpact{}, false
}

// A tool set change Claude Code announces after the prefix leaves the
// cache alone: measured, not a finding. One followed by a rebuild of the
// prefix, with no idle gap, model switch or compaction to explain it, is.
func TestLintMeasuresToolSetChangesMidSession(t *testing.T) {
	cases := []struct {
		name         string
		after        func(s *session)
		wantRebuilds int
		wantExpl     int
		wantAfter    float64 // 0: no share
		wantFinding  bool
	}{
		{"appended delta keeps the cache", func(s *session) { s.turn(10*time.Second, 101_000, 500) }, 0, 0, 101_000.0 / 101_503, false},
		{"delta then the prefix is rebuilt", func(s *session) { s.turn(10*time.Second, 20_000, 81_500) }, 1, 0, 20_000.0 / 101_503, true},
		{"rebuild after an idle gap is the lifetime's", func(s *session) { s.turn(2*time.Hour, 20_000, 81_500) }, 0, 1, 0, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fx := newFixture(t)
			root := filepath.Join(fx.home, ".claude", "projects")
			fx.src.TranscriptRoot = root
			s := newSession(t, "s1")
			// At startup the whole set arrives before any turn: not mid-session.
			s.attach(toolsDelta([]string{"Read", "mcp__a__x"}, nil))
			s.turn(time.Second, 20_000, 80_000)
			s.sidechainTurn(0, 9_000)
			s.turn(10*time.Second, 100_000, 1_000)
			s.attach(toolsDelta(nil, nil)) // nothing changed
			s.attach(toolsDelta([]string{"mcp__b__y"}, nil))
			s.attach(map[string]any{"type": "mcp_instructions_delta", "addedNames": []string{"b"}, "addedBlocks": []string{"private instructions"}})
			c.after(s)
			s.write(root)

			r := Lint(fx.src)
			got, ok := impactOf(r, KindToolSetChange)
			if !ok || got.Changes != 1 || got.Sessions != 1 || got.Rebuilds != c.wantRebuilds || got.Explained != c.wantExpl {
				t.Fatalf("impact = %+v (%v), want 1 change in 1 session, %d rebuilds, %d explained", got, ok, c.wantRebuilds, c.wantExpl)
			}
			switch {
			case c.wantAfter == 0:
				if got.CacheReadShareBefore != nil || got.CacheReadShareAfter != nil {
					t.Errorf("an explained rebuild is left out of the shares: %+v", got)
				}
			case got.CacheReadShareBefore == nil || *got.CacheReadShareBefore != 100_000.0/101_003:
				t.Errorf("share before = %v", got.CacheReadShareBefore)
			case got.CacheReadShareAfter == nil || *got.CacheReadShareAfter != c.wantAfter:
				t.Errorf("share after = %v, want %v", got.CacheReadShareAfter, c.wantAfter)
			}
			fs := findingsOf(r, KindToolSetChange)
			if !c.wantFinding {
				if len(fs) != 0 {
					t.Errorf("findings = %+v, want none", fs)
				}
				return
			}
			if len(fs) != 1 {
				t.Fatalf("findings = %+v, want one", r.Findings)
			}
			f := fs[0]
			if f.Severity != SeverityMedium || f.Source != SourceSession || f.Token != "1 of 1 changes followed by a rebuild" ||
				f.Why == "" || f.Fix == "" || !f.InPrefix() || !f.LoadedBy(ClientClaudeCode) {
				t.Errorf("finding = %+v", f)
			}
			if f.Subject() != "tool set changing mid-session" {
				t.Errorf("subject = %q", f.Subject())
			}
		})
	}
}

// An always-loaded file edited during a session is joined by path: the
// files the session's instructions listed, and CLAUDE.md or AGENTS.md
// wherever they are. The agent's own Edit counts as much as one from
// outside, and two notices of one edit are one change.
func TestLintMeasuresContextEditsMidSession(t *testing.T) {
	fx := newFixture(t)
	root := filepath.Join(fx.home, ".claude", "projects")
	fx.src.TranscriptRoot = root
	memory := filepath.Join(fx.home, ".claude", "projects", "p", "memory", "MEMORY.md")
	claudeMD := filepath.Join(fx.project, "CLAUDE.md")

	s := newSession(t, "s1")
	s.attach(map[string]any{"type": "instructions", "files": []map[string]any{
		{"path": memory, "type": "AutoMem", "content": "private memory"}, {"path": claudeMD, "type": "Project", "content": "private rules"}}})
	s.turn(time.Second, 20_000, 80_000)
	s.turn(10*time.Second, 100_000, 1_000, map[string]any{"type": "tool_use", "name": "Edit",
		"input": map[string]any{"file_path": claudeMD, "old_string": "private", "new_string": "words"}})
	s.attach(map[string]any{"type": "edited_text_file", "filename": claudeMD, "snippet": "private snippet"})
	s.turn(5*time.Second, 101_000, 600) // kept: Claude Code appends the change
	s.attach(map[string]any{"type": "edited_text_file", "filename": memory, "snippet": ""})
	s.turn(5*time.Second, 10_000, 92_000) // rebuilt
	s.attach(map[string]any{"type": "edited_text_file", "filename": filepath.Join(fx.project, "main.go"), "snippet": ""})
	s.turn(5*time.Second, 102_000, 300) // not a context file
	s.write(root)

	r := Lint(fx.src)
	got, ok := impactOf(r, KindContextEdit)
	if !ok || got.Changes != 2 || got.Sessions != 1 || got.Rebuilds != 1 {
		t.Fatalf("impact = %+v, want 2 edits, 1 rebuild", got)
	}
	fs := findingsOf(r, KindContextEdit)
	if len(fs) != 1 {
		t.Fatalf("findings = %+v, want MEMORY.md's", r.Findings)
	}
	f := fs[0]
	if f.Path != memory || f.Token != "1 of 1 edits followed by a rebuild" || f.Severity != SeverityMedium {
		t.Errorf("finding = %+v", f)
	}
	if f.Subject() != "`MEMORY.md` edited mid-session" {
		t.Errorf("subject = %q", f.Subject())
	}
	b, _ := json.Marshal(r)
	for _, w := range []string{"private", "words"} {
		if strings.Contains(string(b), w) {
			t.Errorf("report carries transcript text %q", w)
		}
	}
}

func TestLintWithoutTranscriptsMeasuresNothing(t *testing.T) {
	fx := newFixture(t)
	r := Lint(fx.src)
	if r.Changes == nil || len(r.Changes) != 0 {
		t.Errorf("changes = %+v, want an empty list", r.Changes)
	}
}
