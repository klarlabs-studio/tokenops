package claudecodejsonl

import (
	"strings"
	"testing"
)

// Claude Code writes one JSONL row per assistant turn, and a single user
// prompt fans out into many of them. The vendor's rate-limit meter counts
// USER MESSAGES, so the reader must mark which assistant turn opened a
// new one — otherwise there is nothing in the event stream to count and
// the plan window meter reads 0/200 forever.
func TestFirstTurnAfterUserPromptIsMarked(t *testing.T) {
	lines := []string{
		`{"type":"user","timestamp":"2026-08-23T10:00:00Z","sessionId":"s","message":{"content":"fix the bug"}}`,
		`{"type":"assistant","timestamp":"2026-08-23T10:00:01Z","sessionId":"s","message":{"id":"a1","model":"claude-opus-4-8","usage":{"output_tokens":10}}}`,
		`{"type":"assistant","timestamp":"2026-08-23T10:00:02Z","sessionId":"s","message":{"id":"a2","model":"claude-opus-4-8","usage":{"output_tokens":10}}}`,
		`{"type":"user","timestamp":"2026-08-23T10:01:00Z","sessionId":"s","message":{"content":"now ship it"}}`,
		`{"type":"assistant","timestamp":"2026-08-23T10:01:01Z","sessionId":"s","message":{"id":"a3","model":"claude-opus-4-8","usage":{"output_tokens":10}}}`,
	}
	var got []Turn
	if err := readReader(strings.NewReader(strings.Join(lines, "\n")), "proj", func(tn Turn) error {
		got = append(got, tn)
		return nil
	}); err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("turns = %d, want 3", len(got))
	}
	want := map[string]bool{"a1": true, "a2": false, "a3": true}
	for _, tn := range got {
		if tn.StartsUserMessage != want[tn.MessageID] {
			t.Errorf("%s StartsUserMessage = %v, want %v",
				tn.MessageID, tn.StartsUserMessage, want[tn.MessageID])
		}
	}
}

// Most `type:"user"` rows are tool-result echoes the agent generates on
// the operator's behalf, not prompts the operator typed. In a real
// session they outnumber genuine prompts ~44:1, so counting them would
// inflate the meter just as badly as counting assistant turns.
func TestToolResultEchoesDoNotOpenAMessage(t *testing.T) {
	lines := []string{
		`{"type":"user","timestamp":"2026-08-23T10:00:00Z","sessionId":"s","message":{"content":"go"}}`,
		`{"type":"assistant","timestamp":"2026-08-23T10:00:01Z","sessionId":"s","message":{"id":"a1","model":"m","usage":{"output_tokens":5}}}`,
		`{"type":"user","timestamp":"2026-08-23T10:00:02Z","sessionId":"s","message":{"content":[{"type":"tool_result","content":"ok"}]}}`,
		`{"type":"assistant","timestamp":"2026-08-23T10:00:03Z","sessionId":"s","message":{"id":"a2","model":"m","usage":{"output_tokens":5}}}`,
	}
	var got []Turn
	if err := readReader(strings.NewReader(strings.Join(lines, "\n")), "proj", func(tn Turn) error {
		got = append(got, tn)
		return nil
	}); err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("turns = %d, want 2", len(got))
	}
	if !got[0].StartsUserMessage {
		t.Errorf("a1 should open a user message")
	}
	if got[1].StartsUserMessage {
		t.Errorf("a2 followed a tool_result echo and must not open a message")
	}
}

// A prompt mixing text with an attachment is still a prompt.
func TestMixedContentPromptOpensAMessage(t *testing.T) {
	lines := []string{
		`{"type":"user","timestamp":"2026-08-23T10:00:00Z","sessionId":"s","message":{"content":[{"type":"image"},{"type":"text","text":"what is this"}]}}`,
		`{"type":"assistant","timestamp":"2026-08-23T10:00:01Z","sessionId":"s","message":{"id":"a1","model":"m","usage":{"output_tokens":5}}}`,
	}
	var got []Turn
	if err := readReader(strings.NewReader(strings.Join(lines, "\n")), "proj", func(tn Turn) error {
		got = append(got, tn)
		return nil
	}); err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(got) != 1 || !got[0].StartsUserMessage {
		t.Errorf("mixed-content prompt should open a message: %+v", got)
	}
}

// A prompt whose first assistant turn carries no usage (a no-op echo) must
// carry the boundary forward to the next real turn rather than losing it.
func TestBoundarySurvivesZeroUsageTurn(t *testing.T) {
	lines := []string{
		`{"type":"user","timestamp":"2026-08-23T10:00:00Z","sessionId":"s","message":{"content":"go"}}`,
		`{"type":"assistant","timestamp":"2026-08-23T10:00:01Z","sessionId":"s","message":{"id":"skip","model":"m","usage":{"output_tokens":0}}}`,
		`{"type":"assistant","timestamp":"2026-08-23T10:00:02Z","sessionId":"s","message":{"id":"a1","model":"m","usage":{"output_tokens":7}}}`,
	}
	var got []Turn
	if err := readReader(strings.NewReader(strings.Join(lines, "\n")), "proj", func(tn Turn) error {
		got = append(got, tn)
		return nil
	}); err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("turns = %d, want 1", len(got))
	}
	if !got[0].StartsUserMessage {
		t.Errorf("boundary lost across a zero-usage turn")
	}
}
