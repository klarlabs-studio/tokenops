package claudecodejsonl

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// Claude Code writes a subagent's turns to its own transcript beside the
// session's: <project>/<session>/subagents/agent-<id>.jsonl, with what
// started it in agent-<id>.meta.json. Those turns are the session's spend
// too, so they are read, once each, under the session's project and
// workflow, and marked as the subagent's.
func TestPollerReadsSubagentTranscripts(t *testing.T) {
	root := t.TempDir()
	proj := filepath.Join(root, "-Users-me-src-app")
	sub := filepath.Join(proj, "s1", "subagents")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(path, body string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(proj, "s1.jsonl"), sampleTurn+"\n")
	write(filepath.Join(sub, "agent-a1.jsonl"),
		`{"type":"user","isSidechain":true,"agentId":"a1","timestamp":"2026-05-14T09:23:00.000Z","sessionId":"s1","message":{"content":"Find the config loader"}}`+"\n"+
			`{"type":"assistant","isSidechain":true,"agentId":"a1","timestamp":"2026-05-14T09:23:05.000Z","sessionId":"s1","message":{"id":"msg_sub1","model":"claude-haiku-4-5","usage":{"input_tokens":5,"output_tokens":7,"cache_read_input_tokens":300,"cache_creation_input_tokens":0}}}`+"\n")
	write(filepath.Join(sub, "agent-a1.meta.json"), `{"agentType":"Explore","description":"Find the config loader","toolUseId":"toolu_1","spawnDepth":1}`)
	write(filepath.Join(sub, "agent-a2.jsonl"),
		`{"type":"assistant","isSidechain":true,"agentId":"a2","timestamp":"2026-05-14T09:24:00.000Z","sessionId":"s1","message":{"id":"msg_sub2","model":"claude-sonnet-4-5","usage":{"input_tokens":1,"output_tokens":2,"cache_read_input_tokens":0,"cache_creation_input_tokens":0}}}`+"\n")

	bus := &captureBus{}
	p := NewPoller(bus, PollerOptions{Root: root, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	p.scan(context.Background(), root)
	p.scan(context.Background(), root)

	byMessage := map[string]*eventschema.Envelope{}
	for _, env := range bus.envelopes {
		byMessage[env.Attributes["message_id"]] = env
	}
	if len(bus.envelopes) != 3 || len(byMessage) != 3 {
		t.Fatalf("envelopes = %d (%d messages), want 3 once each", len(bus.envelopes), len(byMessage))
	}
	main, sub1, sub2 := byMessage["msg_a"], byMessage["msg_sub1"], byMessage["msg_sub2"]
	if main.Attributes["subagent_id"] != "" {
		t.Errorf("the session's own turn is marked a subagent's: %v", main.Attributes)
	}
	pe := sub1.Payload.(*eventschema.PromptEvent)
	if pe.AgentID != "claude-code:-Users-me-src-app" || pe.WorkflowID != "claude-code:-Users-me-src-app:s1" || pe.SessionID != "s1" {
		t.Errorf("subagent turn attributed to %q / %q / %q", pe.AgentID, pe.WorkflowID, pe.SessionID)
	}
	if got := sub1.Attributes; got["subagent_id"] != "a1" || got["subagent_type"] != "Explore" || got["subagent_tool_use_id"] != "toolu_1" {
		t.Errorf("subagent attributes = %v", got)
	}
	// The prompt that started a subagent was the agent's, not the
	// operator's, so it opens no vendor message.
	if sub1.Attributes["starts_user_message"] != "false" {
		t.Errorf("a subagent's prompt counted as the operator's message")
	}
	if got := sub2.Attributes; got["subagent_id"] != "a2" || got["subagent_type"] != "" {
		t.Errorf("subagent without meta = %v", got)
	}
}
