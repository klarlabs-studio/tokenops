package tools

import (
	"reflect"
	"testing"
)

// A caller that reads a transcript itself gets, line by line, what
// Extract gets from the file.
func TestParseLineReadsWhatExtractReads(t *testing.T) {
	lines := []string{
		`{"timestamp":"2026-05-16T10:00:00Z","sessionId":"s","message":{"content":[{"type":"text","text":"on it"},{"type":"tool_use","id":"tu1","name":"Bash","input":{"command":"rm -rf build"}},{"type":"tool_use","id":"tu2","name":"Read","input":{"file_path":"/x.go"}}]}}`,
		`{"timestamp":"2026-05-16T10:00:01Z","sessionId":"s","message":{"content":[{"type":"tool_result","tool_use_id":"tu1","is_error":false,"content":"ok"},{"type":"tool_result","tool_use_id":"tu2","is_error":true,"content":"no such file"}]}}`,
		`{"timestamp":"2026-05-16T10:00:02Z","sessionId":"s","message":{"content":"a prompt about tool_use, typed"}}`,
		`{"timestamp":"2026-05-16T10:00:03Z","sessionId":"s","message":{"content":[{"type":"text","text":"no tool here"}]}}`,
		`{"timestamp":"never","sessionId":"s","message":{"content":[{"type":"tool_use","id":"tu3","name":"Bash","input":{"command":"ls"}}]}}`,
		`{ "timestamp" : "2026-05-16T10:00:04Z", "sessionId" : "s", "message" : { "content" : [ { "type" : "tool_use", "id" : "tu4", "name" : "Edit" } ] } }`,
		`tool_use, but not json`,
		``,
	}
	dir := t.TempDir()
	_ = writeJSONL(t, dir, "sess.jsonl", lines...)
	want, err := Extract(ExtractOptions{Root: dir})
	if err != nil {
		t.Fatal(err)
	}
	if len(want) != 5 {
		t.Fatalf("the fixture holds %d tool events, want 5", len(want))
	}
	got := make([]ToolEvent, 0, len(want))
	for _, l := range lines {
		got = append(got, ParseLine([]byte(l))...)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("parsed\n%+v\nExtract read\n%+v", got, want)
	}
}

// Destructive is what Analyze counts as a destructive call: a Bash call,
// never its result and never another tool carrying the same words.
func TestDestructiveIsWhatAnalyzeCounts(t *testing.T) {
	for _, c := range []struct {
		ev   ToolEvent
		want bool
	}{
		{ToolEvent{Name: "Bash", RawCommand: "rm -rf build"}, true},
		{ToolEvent{Name: "Bash", RawCommand: "git push --force"}, true},
		{ToolEvent{Name: "Bash", RawCommand: "go test ./..."}, false},
		{ToolEvent{Name: "Bash"}, false},
		{ToolEvent{Name: "Write", RawCommand: "rm -rf build"}, false},
		{ToolEvent{Name: "Bash", RawCommand: "rm -rf build", IsResult: true}, false},
	} {
		if got := c.ev.Destructive(); got != c.want {
			t.Errorf("%+v destructive = %v, want %v", c.ev, got, c.want)
		}
		counted := Analyze([]ToolEvent{c.ev}).DestructiveCalls == 1
		if counted != c.want {
			t.Errorf("Analyze counted %+v as destructive: %v, want %v", c.ev, counted, c.want)
		}
	}
}
