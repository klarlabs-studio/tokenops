package boundaries

import (
	"strings"
	"testing"
)

func TestSubagentConditions(t *testing.T) {
	rules, err := Parse([]RuleSpec{
		{Name: "fan-out", Action: "subagent_model", When: map[string]string{"prompt_subagents": "> 5"}, Require: "ask", Stage: "enforce"},
		{Name: "many", Action: "subagent_model", When: map[string]string{"session_subagents": ">= 30"}, Require: "deny", Stage: "enforce"},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name  string
		fig   SubagentFigure
		binds string
	}{
		{"unknown", SubagentFigure{}, ""},
		{"within", SubagentFigure{Known: true, Session: 12, Prompt: 5, PromptKnown: true}, ""},
		{"prompt fan-out", SubagentFigure{Known: true, Session: 12, Prompt: 6, PromptKnown: true}, "fan-out"},
		{"no prompt found", SubagentFigure{Known: true, Session: 12, Prompt: 6}, ""},
		{"session cap is stricter", SubagentFigure{Known: true, Session: 30, Prompt: 6, PromptKnown: true}, "many"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := Evaluate(rules, ActionSubagentModel, Facts{Subagents: tc.fig})
			got := ""
			if v.Binding != nil {
				got = v.Binding.Rule.Name
			}
			if got != tc.binds {
				t.Fatalf("binding = %q, want %q", got, tc.binds)
			}
		})
	}
	v := Evaluate(rules, ActionSubagentModel, Facts{Subagents: SubagentFigure{Known: true, Session: 7, Prompt: 6, PromptKnown: true}})
	if got, want := v.Binding.Crossed(), "subagent 6 since the last prompt (> 5)"; got != want {
		t.Fatalf("Crossed = %q, want %q", got, want)
	}
	if x := v.Binding.Crossings(); len(x) != 1 || x[0].Field != FieldPromptSubagents || x[0].Value != 6 || x[0].Threshold != 5 {
		t.Fatalf("Crossings = %+v", x)
	}
}

func TestSubagentConditionsParse(t *testing.T) {
	for _, tc := range []struct {
		action, when, want string
	}{
		{"continue", "> 3", "does not apply to continue rules"},
		{"subagent_model", "> lots", "is not a whole number"},
		{"subagent_model", "> 1001", "must be between 0 and 1000"},
		{"subagent_model", "3", "needs a comparator"},
	} {
		_, err := Parse([]RuleSpec{{Name: "r", Action: tc.action, When: map[string]string{"prompt_subagents": tc.when}, Require: "ask"}})
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s %q: err = %v, want %q", tc.action, tc.when, err, tc.want)
		}
	}
}
