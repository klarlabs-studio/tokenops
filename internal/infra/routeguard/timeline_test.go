package routeguard

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// timelineTranscript is a parent session that launches three subagents:
// one inherits the session's opus and fails, one asks for sonnet and is
// reworked by the parent, and one the operator declines at the prompt.
// A sidechain entry (an older transcript layout) must not count.
const timelineTranscript = `{"type":"user","timestamp":"2026-10-20T12:00:00Z","sessionId":"s1","message":{"role":"user","content":"fix the parser and add tests"}}
{"type":"assistant","timestamp":"2026-10-20T12:00:10Z","sessionId":"s1","message":{"role":"assistant","model":"claude-opus-5","content":[{"type":"tool_use","id":"tu1","name":"Agent","input":{"description":"Fix the parser bug","prompt":"Fix the off-by-one in internal/parse/parser.go"}}]}}
{"type":"user","timestamp":"2026-10-20T12:02:00Z","sessionId":"s1","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"tu1","is_error":true,"content":"agent crashed"}]}}
{"type":"assistant","timestamp":"2026-10-20T12:03:00Z","sessionId":"s1","message":{"role":"assistant","model":"claude-opus-5","content":[{"type":"tool_use","id":"tu2","name":"Agent","input":{"description":"Add lexer tests","prompt":"Write tests","model":"sonnet"}}]}}
{"type":"assistant","timestamp":"2026-10-20T12:03:30Z","sessionId":"s1","isSidechain":true,"message":{"role":"assistant","model":"claude-sonnet-5","content":[{"type":"tool_use","id":"side1","name":"Edit","input":{"file_path":"/repo/internal/parse/lexer_test.go"}}]}}
{"type":"user","timestamp":"2026-10-20T12:05:00Z","sessionId":"s1","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"tu2","content":[{"type":"text","text":"Added internal/parse/lexer_test.go with 4 cases."}]}]}}
{"type":"assistant","timestamp":"2026-10-20T12:06:00Z","sessionId":"s1","message":{"role":"assistant","model":"claude-opus-5","content":[{"type":"tool_use","id":"e1","name":"Edit","input":{"file_path":"/repo/internal/parse/lexer_test.go"}}]}}
{"type":"assistant","timestamp":"2026-10-20T12:07:00Z","sessionId":"s1","message":{"role":"assistant","model":"claude-opus-5","content":[{"type":"tool_use","id":"tu3","name":"Agent","input":{"description":"Refactor the cache","prompt":"refactor"}}]}}
{"type":"user","timestamp":"2026-10-20T12:07:05Z","sessionId":"s1","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"tu3","is_error":true,"content":"The user doesn't want to proceed with this tool use."}]}}
{"type":"user","timestamp":"2026-10-20T12:07:06Z","sessionId":"s1","message":{"role":"user","content":[{"type":"text","text":"[Request interrupted by user for tool use]"}]}}
{"type":"user","timestamp":"2026-10-20T12:08:00Z","sessionId":"s1","message":{"role":"user","content":"<command-name>/compact</command-name>"}}
{"type":"user","timestamp":"2026-10-20T12:09:00Z","sessionId":"s1","message":{"role":"user","content":"no, that's wrong, try again"}}
`

func TestReadTimelineReadsSubagentsAndWhatFollowed(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "-repo-project")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "s1.jsonl")
	if err := os.WriteFile(path, []byte(timelineTranscript), 0o600); err != nil {
		t.Fatal(err)
	}
	tl, start := ReadTimeline(path)
	if tl.Session != "s1" || tl.Scope != "-repo-project" {
		t.Fatalf("session/scope = %q/%q", tl.Session, tl.Scope)
	}
	if start.Model != "opus" || !start.At.Equal(time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)) {
		t.Fatalf("start = %+v", start)
	}
	if len(tl.Calls) != 3 {
		t.Fatalf("calls = %+v", tl.Calls)
	}
	c1, c2, c3 := tl.Calls[0], tl.Calls[1], tl.Calls[2]
	if c1.Ref != "tu1" || c1.Model != "opus" || c1.Kind == "" || !c1.Failed || c1.Excluded || !c1.HasResult {
		t.Errorf("inherited, failed call = %+v", c1)
	}
	if !strings.Contains(strings.Join(c1.Mentions, " "), "internal/parse/parser.go") {
		t.Errorf("mentions = %v", c1.Mentions)
	}
	if c2.Model != "sonnet" || c2.Failed || !strings.Contains(strings.Join(c2.Mentions, " "), "internal/parse/lexer_test.go") {
		t.Errorf("sonnet call = %+v", c2)
	}
	if !c3.Excluded {
		t.Errorf("declined call not excluded: %+v", c3)
	}
	if len(tl.Edits) != 1 || tl.Edits[0].File != "/repo/internal/parse/lexer_test.go" {
		t.Errorf("edits = %+v (the sidechain edit must not count)", tl.Edits)
	}
	if len(tl.Interrupts) != 1 {
		t.Errorf("interrupts = %v", tl.Interrupts)
	}
	if !tl.ModelSwitch.IsZero() {
		t.Errorf("model switch read where there was none: %v", tl.ModelSwitch)
	}
	if s := ReadStart(path); s != start {
		t.Errorf("ReadStart = %+v, want %+v", s, start)
	}
	if len(tl.Prompts) != 2 || tl.Prompts[0].Rejects || !tl.Prompts[1].Rejects {
		t.Errorf("prompts = %+v (the slash command is not a prompt; the last one rejects)", tl.Prompts)
	}
}

func TestReadTimelineMissingFile(t *testing.T) {
	tl, start := ReadTimeline(filepath.Join(t.TempDir(), "none.jsonl"))
	if len(tl.Calls) != 0 || !start.At.IsZero() {
		t.Fatalf("missing file read as %+v %+v", tl, start)
	}
}

// A /model switch mid-session is recorded, so the session's evidence for
// its starting model ends there.
func TestReadTimelineNotesAModelSwitch(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	body := `{"type":"assistant","timestamp":"2026-10-20T12:00:00Z","sessionId":"s","message":{"model":"claude-sonnet-5","content":[]}}
{"type":"assistant","timestamp":"2026-10-20T12:05:00Z","sessionId":"s","isSidechain":true,"message":{"model":"claude-haiku-4-5","content":[]}}
{"type":"assistant","timestamp":"2026-10-20T12:10:00Z","sessionId":"s","message":{"model":"claude-opus-5","content":[]}}
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	tl, start := ReadTimeline(path)
	if start.Model != "sonnet" || !tl.ModelSwitch.Equal(time.Date(2026, 10, 20, 12, 10, 0, 0, time.UTC)) {
		t.Fatalf("start %+v, switch %v", start, tl.ModelSwitch)
	}
}
