package agentdx

import "testing"

// Claude Code writes an assistant message as one line per content block,
// and each line repeats the message's usage. The lines below are one
// instruction answered by two messages: the first in three lines.
var twoMessagesInFourLines = []string{
	`{"type":"user","timestamp":"2026-08-23T10:00:00Z","sessionId":"s","message":{"content":"fix the bug"}}`,
	`{"type":"assistant","timestamp":"2026-08-23T10:00:01Z","sessionId":"s","requestId":"req_1","message":{"id":"msg_1","model":"claude-opus-5","content":[{"type":"text","text":"looking"}],"usage":{"input_tokens":10,"cache_read_input_tokens":500,"cache_creation_input_tokens":40}}}`,
	`{"type":"assistant","timestamp":"2026-08-23T10:00:02Z","sessionId":"s","requestId":"req_1","message":{"id":"msg_1","model":"claude-opus-5","content":[{"type":"tool_use","id":"t1","name":"Read","input":{"file_path":"/a.go"}}],"usage":{"input_tokens":10,"cache_read_input_tokens":500,"cache_creation_input_tokens":40}}}`,
	`{"type":"assistant","timestamp":"2026-08-23T10:00:03Z","sessionId":"s","requestId":"req_1","message":{"id":"msg_1","model":"claude-opus-5","content":[{"type":"tool_use","id":"t2","name":"Read","input":{"file_path":"/b.go"}}],"usage":{"input_tokens":10,"cache_read_input_tokens":500,"cache_creation_input_tokens":40}}}`,
	`{"type":"assistant","timestamp":"2026-08-23T10:00:09Z","sessionId":"s","requestId":"req_2","message":{"id":"msg_2","model":"claude-opus-5","content":[{"type":"text","text":"done"}],"usage":{"input_tokens":10,"cache_read_input_tokens":600,"cache_creation_input_tokens":40}}}`,
}

func TestUnitsCountAMessageOnce(t *testing.T) {
	units := Units(recordsFrom(t, twoMessagesInFourLines...))
	if len(units) != 1 {
		t.Fatalf("units = %d, want 1", len(units))
	}
	u := units[0]
	if u.Turns != 2 {
		t.Errorf("Turns = %d, want 2: two messages, one of them in three lines", u.Turns)
	}
	if want := int64(550 + 650); u.Tokens != want {
		t.Errorf("Tokens = %d, want %d: each message's context once", u.Tokens, want)
	}
	if u.ToolCalls != 2 {
		t.Errorf("ToolCalls = %d, want 2: the calls on a message's later lines still count", u.ToolCalls)
	}
}

// A line that carries a message id and no request id belongs to that
// message all the same.
func TestExtractGroupsLinesByMessageWithoutARequestID(t *testing.T) {
	units := Units(recordsFrom(t,
		`{"type":"user","timestamp":"2026-08-23T10:00:00Z","sessionId":"s","message":{"content":"fix the bug"}}`,
		`{"type":"assistant","timestamp":"2026-08-23T10:00:01Z","sessionId":"s","message":{"id":"msg_1","content":[{"type":"text","text":"a"}],"usage":{"input_tokens":100}}}`,
		`{"type":"assistant","timestamp":"2026-08-23T10:00:02Z","sessionId":"s","message":{"id":"msg_1","content":[{"type":"text","text":"b"}],"usage":{"input_tokens":100}}}`,
	))
	if len(units) != 1 || units[0].Turns != 1 || units[0].Tokens != 100 {
		t.Fatalf("want one unit of one turn and 100 tokens, got %+v", units)
	}
}

func TestContextCurveCountsAMessageOnce(t *testing.T) {
	lines := append([]string{
		// The context the instruction starts in.
		`{"type":"assistant","timestamp":"2026-08-23T09:59:59Z","sessionId":"s","requestId":"req_0","message":{"id":"msg_0","usage":{"input_tokens":450000}}}`,
	}, twoMessagesInFourLines...)
	bands := ComputeContextCurve(recordsFrom(t, lines...))
	for _, b := range bands {
		if !b.Contains(450_000) {
			continue
		}
		if b.Prompts != 1 || b.MedianTurns != 2 {
			t.Fatalf("want one instruction of two turns, got %+v", b)
		}
		return
	}
	t.Fatalf("no band holds the instruction: %+v", bands)
}
