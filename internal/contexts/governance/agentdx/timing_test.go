package agentdx

import (
	"strings"
	"testing"
	"time"
)

// Concurrent sessions interleave in wall-clock time. Building units in one
// global time order handed session a's turn and tool call to session b,
// because b's instruction was typed last.
func TestUnitsDoNotLendTurnsAcrossConcurrentSessions(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
	recs := []Record{
		{At: t0, SessionID: "a", Kind: KindPrompt},
		{At: t0.Add(time.Second), SessionID: "b", Kind: KindPrompt},
		{At: t0.Add(2 * time.Second), SessionID: "a", Kind: KindAssistantTurn},
		{At: t0.Add(3 * time.Second), SessionID: "a", Kind: KindToolUse, ToolName: "Read"},
	}
	units := Units(recs)
	if len(units) != 2 {
		t.Fatalf("units = %d, want 2", len(units))
	}
	for _, u := range units {
		want := 0
		if u.SessionID == "a" {
			want = 1
		}
		if u.Turns != want || u.ToolCalls != want {
			t.Errorf("session %s: turns=%d tools=%d, want %d each", u.SessionID, u.Turns, u.ToolCalls, want)
		}
	}
	if !units[0].Start.Before(units[1].Start) {
		t.Errorf("units out of chronological order")
	}
}

// A Claude Code transcript records when each tool call was made and when
// its result came back, paired by id, and which entries one model request
// wrote. Those are the intervals the reader keeps; nothing else.
func TestClaudeCodeTimingIsWhatTheTranscriptRecords(t *testing.T) {
	recs := recordsFrom(t,
		`{"type":"user","timestamp":"2026-03-01T10:00:00Z","sessionId":"s","message":{"content":"tidy the parser"}}`,
		// Request r1 decides to call two tools in parallel.
		`{"type":"assistant","timestamp":"2026-03-01T10:00:10Z","sessionId":"s","requestId":"r1","message":{"model":"m","content":[{"type":"tool_use","id":"t1","name":"Bash","input":{}}]}}`,
		`{"type":"assistant","timestamp":"2026-03-01T10:00:12Z","sessionId":"s","requestId":"r1","message":{"model":"m","content":[{"type":"tool_use","id":"t2","name":"Bash","input":{}}]}}`,
		`{"type":"user","timestamp":"2026-03-01T10:00:40Z","sessionId":"s","message":{"content":[{"type":"tool_result","tool_use_id":"t1"}]}}`,
		`{"type":"user","timestamp":"2026-03-01T10:00:52Z","sessionId":"s","message":{"content":[{"type":"tool_result","tool_use_id":"t2"}]}}`,
		// Request r2 answers once both results are in.
		`{"type":"assistant","timestamp":"2026-03-01T10:01:02Z","sessionId":"s","requestId":"r2","message":{"model":"m","content":[{"type":"text","text":"done"}]}}`,
		`{"type":"system","subtype":"turn_duration","timestamp":"2026-03-01T10:01:03Z","sessionId":"s","durationMs":63000}`,
	)
	units := Units(recs)
	if len(units) != 1 {
		t.Fatalf("units = %d", len(units))
	}
	u := units[0]
	if u.ToolCalls != 2 || u.MatchedToolCalls != 2 {
		t.Errorf("tool calls %d matched %d, want 2 and 2", u.ToolCalls, u.MatchedToolCalls)
	}
	if u.ModelRequests != 2 || u.TimedModelRequests != 2 {
		t.Errorf("requests %d timed %d, want 2 and 2", u.ModelRequests, u.TimedModelRequests)
	}
	if !u.Completed || u.HarnessTurns != 1 || u.HarnessTurnMs != 63000 {
		t.Errorf("completion = %v, %d turns, %dms; want the recorded turn end", u.Completed, u.HarnessTurns, u.HarnessTurnMs)
	}
	got := map[SpanKind][]time.Duration{}
	for _, s := range u.Spans {
		got[s.Kind] = append(got[s.Kind], s.End.Sub(s.Start))
	}
	if d := got[SpanTool]; len(d) != 2 || d[0] != 30*time.Second || d[1] != 40*time.Second {
		t.Errorf("tool spans = %v, want 30s and 40s", d)
	}
	// r1: instruction 10:00:00 → its last entry 10:00:12. r2: the later of
	// the last result (10:00:52) and r1's end → 10:01:02.
	if d := got[SpanModel]; len(d) != 2 || d[0] != 12*time.Second || d[1] != 10*time.Second {
		t.Errorf("model spans = %v, want 12s and 10s", d)
	}
	if u.TTFTRecorded {
		t.Errorf("TTFT recorded from a Claude Code transcript, which carries none")
	}
}

// A question to the operator is time spent waiting for a person, not for
// a machine.
func TestOperatorQuestionIsAWaitNotATool(t *testing.T) {
	recs := recordsFrom(t,
		`{"type":"user","timestamp":"2026-03-01T10:00:00Z","sessionId":"s","message":{"content":"plan the migration"}}`,
		`{"type":"assistant","timestamp":"2026-03-01T10:00:05Z","sessionId":"s","requestId":"r1","message":{"content":[{"type":"tool_use","id":"q","name":"AskUserQuestion","input":{}}]}}`,
		`{"type":"user","timestamp":"2026-03-01T10:04:05Z","sessionId":"s","message":{"content":[{"type":"tool_result","tool_use_id":"q"}]}}`,
	)
	u := Units(recs)[0]
	if len(u.Spans) != 2 || u.Spans[1].Kind != SpanOperatorWait {
		t.Fatalf("spans = %+v, want a model span and an operator wait", u.Spans)
	}
}

// An instruction nobody answered has no turns, no spans and no completion.
func TestUnansweredInstructionHasNoTiming(t *testing.T) {
	recs := recordsFrom(t,
		`{"type":"user","timestamp":"2026-03-01T10:00:00Z","sessionId":"s","message":{"content":"are you there"}}`,
	)
	u := Units(recs)[0]
	if u.Turns != 0 || len(u.Spans) != 0 || u.Completed || u.Duration() != 0 {
		t.Errorf("unanswered unit = %+v", u)
	}
}

// Codex records what a session runs as, both kinds of tool call with
// their results, and its own turn duration and time to first token.
func TestCodexRolloutCarriesRoleToolsAndTurnTiming(t *testing.T) {
	review := strings.Join([]string{
		`{"timestamp":"2026-03-01T10:00:00Z","type":"session_meta","payload":{"source":{"subagent":{"other":"guardian"}}}}`,
		`{"timestamp":"2026-03-01T10:00:00Z","type":"turn_context","payload":{"model":"codex-auto-review"}}`,
		`{"timestamp":"2026-03-01T10:00:01Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"review this diff"}]}}`,
		`{"timestamp":"2026-03-01T10:00:03Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[]}}`,
		`{"timestamp":"2026-03-01T10:00:03Z","type":"event_msg","payload":{"type":"task_complete","duration_ms":2000,"time_to_first_token_ms":700}}`,
	}, "\n")
	recs := readCodexTranscript(strings.NewReader(review), "r", time.Time{}, false)
	u := Units(recs)[0]
	if u.Role != RoleAutoReview {
		t.Errorf("role = %q, want auto_review", u.Role)
	}
	if !u.Completed || !u.TTFTRecorded || u.TTFTMs != 700 || u.HarnessTurnMs != 2000 {
		t.Errorf("turn timing = %+v", u)
	}

	work := strings.Join([]string{
		`{"timestamp":"2026-03-01T11:00:00Z","type":"session_meta","payload":{"source":"vscode"}}`,
		`{"timestamp":"2026-03-01T11:00:00Z","type":"turn_context","payload":{"model":"gpt-x"}}`,
		`{"timestamp":"2026-03-01T11:00:01Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"fix the flaky test"}]}}`,
		`{"timestamp":"2026-03-01T11:00:05Z","type":"response_item","payload":{"type":"custom_tool_call","call_id":"c1","name":"apply_patch"}}`,
		`{"timestamp":"2026-03-01T11:00:06Z","type":"response_item","payload":{"type":"custom_tool_call_output","call_id":"c1"}}`,
		`{"timestamp":"2026-03-01T11:00:07Z","type":"response_item","payload":{"type":"function_call","call_id":"c2","name":"shell"}}`,
		`{"timestamp":"2026-03-01T11:00:17Z","type":"response_item","payload":{"type":"function_call_output","call_id":"c2"}}`,
		`{"timestamp":"2026-03-01T11:00:20Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[]}}`,
	}, "\n")
	recs = readCodexTranscript(strings.NewReader(work), "w", time.Time{}, false)
	u = Units(recs)[0]
	if u.Role != RolePrimary || u.ToolCalls != 2 || u.MatchedToolCalls != 2 {
		t.Errorf("work unit = role %q, %d calls, %d matched", u.Role, u.ToolCalls, u.MatchedToolCalls)
	}
	if u.Completed {
		t.Errorf("completed without a task_complete")
	}
	// Codex groups no entries by request, so no model span is invented.
	for _, s := range u.Spans {
		if s.Kind == SpanModel {
			t.Errorf("model span from a rollout that records no request boundaries: %+v", s)
		}
	}
}

// A cohort selects the same instructions whether the metrics are computed
// from records or the units are listed directly.
func TestFilterRecordsMatchesFilterUnits(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
	recs := []Record{
		{At: t0, SessionID: "a", Kind: KindPrompt},
		{At: t0.Add(time.Second), SessionID: "a", Kind: KindAssistantTurn, Model: "codex-auto-review"},
		{At: t0.Add(2 * time.Second), SessionID: "a", Kind: KindPrompt},
		{At: t0.Add(3 * time.Second), SessionID: "a", Kind: KindAssistantTurn, Model: "gpt-x"},
		{At: t0.Add(4 * time.Second), SessionID: "a", Kind: KindToolUse, ToolName: "shell"},
		{At: t0.Add(5 * time.Second), SessionID: "b", Kind: KindCompaction},
		{At: t0.Add(6 * time.Second), SessionID: "b", Kind: KindPrompt, Role: RoleSubagent},
		{At: t0.Add(7 * time.Second), SessionID: "b", Kind: KindAssistantTurn, Model: "gpt-x"},
	}
	for _, c := range []Cohort{{Role: RolePrimary}, {Role: RoleAutoReview}, {Model: "GPT-X"}, {Role: RoleSubagent, Model: "gpt-x"}} {
		want := FilterUnits(Units(recs), c)
		got := Units(FilterRecords(recs, c))
		if len(got) != len(want) {
			t.Errorf("%+v: %d units from filtered records, %d filtered units", c, len(got), len(want))
			continue
		}
		for i := range got {
			if got[i].Start != want[i].Start || got[i].Turns != want[i].Turns || got[i].ToolCalls != want[i].ToolCalls {
				t.Errorf("%+v: unit %d differs: %+v vs %+v", c, i, got[i], want[i])
			}
		}
	}
	if n := Compute(FilterRecords(recs, Cohort{Role: RoleAutoReview})).Prompts; n != 1 {
		t.Errorf("auto-review prompts = %d, want 1", n)
	}
}

func TestParseRole(t *testing.T) {
	for in, want := range map[string]string{"": "", "all": "", "Primary": RolePrimary, "auto-review": RoleAutoReview, "subagent": RoleSubagent} {
		got, err := ParseRole(in)
		if err != nil || got != want {
			t.Errorf("ParseRole(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	if _, err := ParseRole("reviewer"); err == nil {
		t.Errorf("unknown role accepted")
	}
}
