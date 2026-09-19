package mcp

import (
	"encoding/json"
	"strings"
	"testing"
)

// oldTranscript is one instruction from years ago — outside any default
// window, inside "all history".
const oldTranscript = `{"type":"user","timestamp":"2020-01-01T10:00:00Z","sessionId":"s","message":{"content":"rename the handler"}}
{"type":"assistant","timestamp":"2020-01-01T10:00:01Z","sessionId":"s","message":{"usage":{"input_tokens":100},"content":[{"type":"tool_use","name":"Edit","input":{"file_path":"/a.go"}}]}}`

// The schema said "0 reads all history" while the handler turned 0 into
// 7, and an omitted field is also 0 — so the promise was unreachable. all
// is the way to ask for everything.
func TestStoryToolReadsAllHistoryWhenAsked(t *testing.T) {
	res := decodeStory(t, execTool(t, storyServer(t, oldTranscript), "tokenops_story",
		map[string]any{"all": true}))
	if res.Window != "all history" {
		t.Errorf("Window = %q, want all history", res.Window)
	}
	if len(res.Tasks) != 1 {
		t.Fatalf("got %d tasks, want the 2020 instruction", len(res.Tasks))
	}
}

// Omitted still means the default week, which is what keeps an unbounded
// scan from being the accidental default.
func TestStoryToolDefaultsToAWeek(t *testing.T) {
	res := decodeStory(t, execTool(t, storyServer(t, oldTranscript), "tokenops_story", nil))
	if res.Window != "last 7d" || len(res.Tasks) != 0 {
		t.Errorf("Window = %q with %d tasks, want last 7d and none", res.Window, len(res.Tasks))
	}
}

func TestAgentDXToolReadsAllHistoryWhenAsked(t *testing.T) {
	out := execTool(t, dxServer(t, oldTranscript), "tokenops_agent_dx", map[string]any{"all": true})
	var res agentDXResult
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if res.Window != "all history" || res.Metrics.Prompts != 1 {
		t.Errorf("Window = %q with %d prompts, want all history with the 2020 instruction",
			res.Window, res.Metrics.Prompts)
	}
}

// The description is what the agent reads; it must not promise the old
// behaviour.
func TestDaysDescriptionsDoNotPromiseZeroAsAllHistory(t *testing.T) {
	for _, tool := range []string{"tokenops_story", "tokenops_agent_dx"} {
		var srv *Server
		if tool == "tokenops_story" {
			srv = storyServer(t, "")
		} else {
			srv = dxServer(t, "")
		}
		var schema string
		for _, ti := range srv.Tools() {
			if ti.Name == tool {
				b, _ := json.Marshal(ti.InputSchema)
				schema = string(b)
			}
		}
		if strings.Contains(schema, "0 reads all history") {
			t.Errorf("%s schema still promises 0 = all history: %s", tool, schema)
		}
		if !strings.Contains(schema, `"all"`) {
			t.Errorf("%s schema has no all field: %s", tool, schema)
		}
	}
}
