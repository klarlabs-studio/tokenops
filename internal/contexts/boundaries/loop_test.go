package boundaries

import (
	"encoding/json"
	"strings"
	"testing"
)

// seq builds a call sequence from "Tool:args" tokens; the args stand for
// the hash, so equal tokens are equal calls.
func seq(tokens ...string) []ToolCall {
	out := make([]ToolCall, 0, len(tokens))
	for _, tok := range tokens {
		tool, args, _ := strings.Cut(tok, ":")
		out = append(out, ToolCall{Tool: tool, Hash: args})
	}
	return out
}

func TestReadLoop(t *testing.T) {
	cases := []struct {
		name      string
		calls     []ToolCall
		wantCalls int
		period    int
		repeats   int
	}{
		{"nothing yet", nil, 0, 0, 0},
		{"one call", seq("Bash:a"), 0, 0, 0},
		{"the same call four times", seq("Bash:a", "Bash:a", "Bash:a", "Bash:a"), 4, 1, 4},
		{"the same call twice is counted", seq("Read:x", "Bash:a", "Bash:a"), 2, 1, 2},
		{"varied calls stay quiet", seq("Read:a", "Grep:b", "Read:c", "Bash:d", "Read:e", "Bash:f"), 0, 0, 0},
		{"a 2-cycle three times", seq("Bash:t", "Read:r", "Bash:t", "Read:r", "Bash:t", "Read:r"), 6, 2, 3},
		{"a 2-cycle twice is not yet a loop", seq("Bash:t", "Read:r", "Bash:t", "Read:r"), 0, 0, 0},
		{"a 3-cycle three times", seq("Bash:t", "Read:r", "Grep:g", "Bash:t", "Read:r", "Grep:g", "Bash:t", "Read:r", "Grep:g"), 9, 3, 3},
		{"an edit resets the window", seq("Bash:a", "Bash:a", "Bash:a", "Edit:e1", "Bash:a"), 0, 0, 0},
		{"repeats after an edit count again", seq("Bash:a", "Bash:a", "Edit:e1", "Bash:a", "Bash:a", "Bash:a", "Bash:a"), 4, 1, 4},
		{"a write resets it too", seq("Bash:a", "Bash:a", "Bash:a", "Write:w", "Bash:a"), 0, 0, 0},
		{"opencode's lower-case edit resets it", seq("bash:a", "bash:a", "bash:a", "edit:e", "bash:a"), 0, 0, 0},
		{"the same edit again is no progress", seq("Edit:e1", "Bash:t", "Edit:e1", "Bash:t", "Edit:e1", "Bash:t"), 6, 2, 3},
		{"different edits between tests are progress", seq("Edit:e1", "Bash:t", "Edit:e2", "Bash:t", "Edit:e3", "Bash:t"), 0, 0, 0},
		{"the same tool with other args is not a repeat", seq("Read:a", "Read:b", "Read:c", "Read:d"), 0, 0, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ReadLoop(tc.calls)
			if got.Calls != tc.wantCalls || got.Period != tc.period || got.Repeats != tc.repeats {
				t.Fatalf("ReadLoop = %+v; want calls %d, period %d, repeats %d", got, tc.wantCalls, tc.period, tc.repeats)
			}
		})
	}
}

func TestReadLoopNamesTheRepeatedTools(t *testing.T) {
	got := ReadLoop(seq("Bash:t", "Read:r", "Bash:t", "Read:r", "Bash:t", "Read:r"))
	if strings.Join(got.Tools, ",") != "Bash,Read" {
		t.Fatalf("tools = %v; want Bash, Read in the order they ran", got.Tools)
	}
}

func loopRule(t *testing.T, value string) Rule {
	t.Helper()
	r, err := Parse([]RuleSpec{{Name: "loop-breaker", Action: "continue", Require: "ask",
		When: map[string]string{"loop_calls": value}}})
	if err != nil {
		t.Fatal(err)
	}
	return r[0]
}

func TestLoopCallsCondition(t *testing.T) {
	r := loopRule(t, ">= 4")
	loop := Facts{Loop: LoopFigure{Known: true, Reading: ReadLoop(seq("Bash:a", "Bash:a", "Bash:a", "Bash:a"))}}
	m, ok := r.Match(ActionContinue, loop)
	if !ok {
		t.Fatal("a loop of 4 calls did not match loop_calls >= 4")
	}
	if want := "the same Bash call 4 times in a row with no file changed"; !strings.Contains(m.Crossed(), want) {
		t.Errorf("crossed = %q; want %q", m.Crossed(), want)
	}
	if cs := m.Crossings(); len(cs) != 1 || cs[0].Field != FieldLoopCalls || cs[0].Value != 4 || cs[0].Threshold != 4 {
		t.Errorf("crossings = %+v", cs)
	}
	cycle := Facts{Loop: LoopFigure{Known: true, Reading: ReadLoop(seq("Bash:t", "Read:r", "Bash:t", "Read:r", "Bash:t", "Read:r"))}}
	m, ok = r.Match(ActionContinue, cycle)
	if !ok || !strings.Contains(m.Crossed(), "the same 2 calls (Bash, Read) 3 times in a row with no file changed") {
		t.Errorf("cycle: ok=%v crossed=%q", ok, m.Crossed())
	}
	if _, ok := r.Match(ActionContinue, Facts{Loop: LoopFigure{Known: true}}); ok {
		t.Error("no loop matched")
	}
	if _, ok := r.Match(ActionContinue, Facts{}); ok {
		t.Error("an unknown loop figure matched")
	}
}

func TestLoopCallsParse(t *testing.T) {
	for _, bad := range []string{"< 4", "<= 4", "== 4", ">= 1", ">= 65", ">= x"} {
		if _, err := Parse([]RuleSpec{{Name: "l", Action: "continue", Require: "ask", When: map[string]string{"loop_calls": bad}}}); err == nil {
			t.Errorf("loop_calls %q was accepted", bad)
		}
	}
	if _, err := Parse([]RuleSpec{{Name: "l", Action: "spend", Require: "ask", When: map[string]string{"loop_calls": ">= 4"}}}); err == nil {
		t.Error("loop_calls was accepted on a spend rule")
	}
	loopRule(t, "> 5")
}

func TestHashToolInput(t *testing.T) {
	a := HashToolInput(json.RawMessage(`{"command":"go test ./...","description":"Run tests"}`))
	b := HashToolInput(json.RawMessage(`{ "description": "Run the tests again", "command": "go test ./..." }`))
	c := HashToolInput(json.RawMessage(`{"command":"go test ./internal/..."}`))
	if a == "" || a != b {
		t.Errorf("the same command hashed differently: %q %q", a, b)
	}
	if a == c {
		t.Error("different commands hashed the same")
	}
	if strings.Contains(a, "go test") || len(a) != 16 {
		t.Errorf("hash %q is not a short hex digest", a)
	}
	if HashToolInput(nil) != HashToolInput(json.RawMessage(`{}`)) {
		t.Error("no input and an empty one differ")
	}
}
