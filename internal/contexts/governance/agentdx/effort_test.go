package agentdx

import (
	"fmt"
	"testing"
	"time"
)

func assistantTurns(recs []Record) []Record {
	var out []Record
	for _, r := range recs {
		if r.Kind == KindAssistantTurn {
			out = append(out, r)
		}
	}
	return out
}

// Claude Code stamps the session's effort on each assistant entry.
func TestClaudeCodeTurnsCarryModelAndEffort(t *testing.T) {
	turns := assistantTurns(recordsFrom(t,
		`{"type":"user","timestamp":"2026-09-30T10:00:00Z","sessionId":"s","message":{"role":"user","content":"find the config"}}`,
		`{"type":"assistant","timestamp":"2026-09-30T10:00:05Z","sessionId":"s","effort":"High","message":{"role":"assistant","model":"claude-opus-5","content":[],"usage":{"input_tokens":10}}}`,
	))
	if len(turns) != 1 || turns[0].Model != "claude-opus-5" || turns[0].Effort != "high" {
		t.Fatalf("turns = %+v", turns)
	}
}

// Codex opens each turn with a turn_context line; its settings apply to
// the assistant messages that follow.
func TestCodexTurnsTakeTheirTurnContext(t *testing.T) {
	turns := assistantTurns(codexRecords(t,
		`{"timestamp":"2026-09-30T10:00:00Z","type":"turn_context","payload":{"model":"gpt-5.4","effort":"medium"}}`,
		`{"timestamp":"2026-09-30T10:00:01Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"fix the test"}]}}`,
		`{"timestamp":"2026-09-30T10:00:05Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[]}}`,
		`{"timestamp":"2026-09-30T10:01:00Z","type":"turn_context","payload":{"model":"gpt-5.4","effort":"high"}}`,
		`{"timestamp":"2026-09-30T10:01:05Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[]}}`,
	))
	if len(turns) != 2 || turns[0].Effort != "medium" || turns[1].Effort != "high" || turns[1].Model != "gpt-5.4" {
		t.Fatalf("turns = %+v", turns)
	}
}

// opencode records effort as the assistant message's variant.
func TestOpencodeTurnsCarryVariantAsEffort(t *testing.T) {
	path := seedOpencodeDB(t, [][3]any{
		{"m1", "s1", `{"role":"user","time":{"created":1771056604952}}`},
		{"m2", "s1", `{"role":"assistant","time":{"created":1771056613283},"modelID":"big-pickle","variant":"high","tokens":{"input":10}}`},
	}, nil, false)
	recs, err := ExtractOpencode(ExtractOptions{Root: path})
	if err != nil {
		t.Fatal(err)
	}
	turns := assistantTurns(recs)
	if len(turns) != 1 || turns[0].Model != "big-pickle" || turns[0].Effort != "high" {
		t.Fatalf("turns = %+v", turns)
	}
}

// synthUnits writes n one-prompt units in their own session, each served
// at the given model and effort.
func synthUnits(session, model, effort string, n int, start time.Time) []Record {
	out := make([]Record, 0, 2*n)
	for i := range n {
		at := start.Add(time.Duration(i) * time.Minute)
		out = append(out,
			Record{At: at, SessionID: session, Kind: KindPrompt},
			Record{At: at.Add(10 * time.Second), SessionID: session, Kind: KindAssistantTurn, Model: model, Effort: effort, InputTokens: 1000},
		)
	}
	return out
}

func TestByEffortGroupsAndOrders(t *testing.T) {
	t0 := time.Date(2026, 9, 30, 10, 0, 0, 0, time.UTC)
	recs := synthUnits("a", "claude-opus-5", "high", 30, t0)
	// A concurrent session at another effort must not lend its turns.
	recs = append(recs, synthUnits("b", "claude-opus-5", "low", 5, t0.Add(30*time.Second))...)
	// No effort recorded: left out.
	recs = append(recs, synthUnits("c", "claude-opus-5", "", 3, t0)...)

	rows := ByEffort(recs)
	if len(rows) != 2 {
		t.Fatalf("rows = %+v", rows)
	}
	if rows[0].Effort != "low" || rows[1].Effort != "high" {
		t.Errorf("order = %s, %s; want low before high", rows[0].Effort, rows[1].Effort)
	}
	if rows[0].Instructions != 5 || rows[0].Enough {
		t.Errorf("low row = %+v, want 5 instructions, not enough to compare", rows[0])
	}
	if rows[1].Instructions != 30 || !rows[1].Enough || rows[1].MedianTurns != 1 {
		t.Errorf("high row = %+v", rows[1])
	}
}

// A unit is filed under the effort that served most of its turns.
func TestUnitTakesTheMajorityEffort(t *testing.T) {
	t0 := time.Date(2026, 9, 30, 10, 0, 0, 0, time.UTC)
	recs := make([]Record, 0, 4)
	recs = append(recs, Record{At: t0, SessionID: "s", Kind: KindPrompt})
	for i, e := range []string{"high", "medium", "medium"} {
		recs = append(recs, Record{At: t0.Add(time.Duration(i+1) * time.Second), SessionID: "s",
			Kind: KindAssistantTurn, Model: "m", Effort: e})
	}
	units := Units(recs)
	if len(units) != 1 || units[0].Effort != "medium" || units[0].Model != "m" {
		t.Fatalf("units = %s", fmt.Sprint(units))
	}
}
