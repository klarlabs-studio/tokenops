package mcp

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"testing"
)

// Invocation is one tokenops command as the terminal's command tree reads
// it: the command's path below tokenops, its positional words, and the
// flags set, each as its value's text or, for a repeatable flag, the list.
type Invocation struct {
	Path  string
	Args  []string
	Flags map[string]any
}

// ExactCase is a call of a public tool and the commands its shown command
// must run, in order.
type ExactCase struct {
	Tool string
	Args map[string]any
	Want []Invocation
}

// hostile is a value a shell would expand or split if it were not quoted.
const hostile = "it's $HOME `id` \"q\" $(echo x) a;b"

// exactCases cover every route that changes a setting, with every argument
// it takes, and with values that start with a dash or that a shell would
// expand: the command shown must set exactly what the tool applies.
var exactCases = []ExactCase{
	{"tokenops_routing", map[string]any{"action": "decide", "key": "anthropic|claude-sonnet-4|claude-opus-4", "decision": "approve"},
		[]Invocation{{Path: "routing decide", Args: []string{"anthropic|claude-sonnet-4|claude-opus-4", "approve"}}}},
	{"tokenops_routing", map[string]any{"action": "decide", "key": "-anthropic|a|b", "decision": "deny"},
		[]Invocation{{Path: "routing decide", Args: []string{"-anthropic|a|b", "deny"}}}},
	{"tokenops_routing", map[string]any{"action": "set_rule", "provider": "anthropic", "from_model": "claude-opus*", "to_model": "-haiku",
		"quality": 0.9, "fallbacks": []any{"claude-sonnet-4", "odd,name", `q"uote $HOME`, "-dash"}},
		[]Invocation{{Path: "routing rule set", Args: []string{"anthropic", "claude-opus*", "-haiku"},
			Flags: map[string]any{"quality": "0.9", "fallback": []string{"claude-sonnet-4", "odd,name", `q"uote $HOME`, "-dash"}}}}},
	{"tokenops_routing", map[string]any{"action": "set_rule", "provider": "anthropic", "from_model": "-opus", "delete": true},
		[]Invocation{{Path: "routing rule unset", Args: []string{"anthropic", "-opus"}}}},
	{"tokenops_configure", map[string]any{"setting": "plan", "provider": "anthropic", "plan": "claude-enterprise",
		"spend_limit_usd": 5000, "limit_window": "weekly", "rate_factor": 0.8, "price": 214.6, "currency": hostile, "since": "2026-09-01"},
		[]Invocation{{Path: "plan set", Args: []string{"anthropic", "claude-enterprise"}, Flags: map[string]any{
			"spend-limit": "5000", "limit-window": "weekly", "rate-factor": "0.8", "price": "214.6", "currency": hostile, "since": "2026-09-01"}}}},
	{"tokenops_configure", map[string]any{"setting": "plan", "provider": " anthropic ", "plan": "-odd", "rate_factor": -0.5},
		[]Invocation{{Path: "plan set", Args: []string{"anthropic", "-odd"}, Flags: map[string]any{"rate-factor": "-0.5"}}}},
	{"tokenops_configure", map[string]any{"setting": "plan", "provider": "anthropic", "clear": true},
		[]Invocation{{Path: "plan unset", Args: []string{"anthropic"}}}},
	{"tokenops_configure", map[string]any{"setting": "budget", "name": "my budget $HOME", "window": "daily", "limit_usd": 50, "limit_tokens": 1000,
		"warn_at": 0.6, "crit_at": 0.9, "basis": "spend", "workflow_id": "wf 1", "agent_id": "-agent", "tag": "t=1", "provider": "-p",
		"budget_mode": "hard", "require": "deny", "add_window": true},
		[]Invocation{{Path: "budget set", Args: []string{"my budget $HOME"}, Flags: map[string]any{
			"window": "daily", "limit-usd": "50", "limit-tokens": "1000", "warn-at": "0.6", "crit-at": "0.9", "basis": "spend",
			"workflow-id": "wf 1", "agent-id": "-agent", "tag": "t=1", "provider": "-p", "mode": "hard", "require": "deny", "add-window": "true"}}}},
	{"tokenops_configure", map[string]any{"setting": "budget", "name": "-daily", "remove_window": "weekly"},
		[]Invocation{{Path: "budget unset", Args: []string{"-daily"}, Flags: map[string]any{"window": "weekly"}}}},
	{"tokenops_configure", map[string]any{"setting": "budget", "name": hostile, "delete": true},
		[]Invocation{{Path: "budget unset", Args: []string{hostile}}}},
	{"tokenops_configure", map[string]any{"setting": "mode", "mode": "active"},
		[]Invocation{{Path: "mode", Args: []string{"active"}}}},
	{"tokenops_configure", map[string]any{"setting": "preferred_model", "provider": "anthropic", "model": "-opus"},
		[]Invocation{{Path: "preferred-model set", Args: []string{"anthropic", "-opus"}}}},
	{"tokenops_configure", map[string]any{"setting": "preferred_model", "provider": "anthropic", "clear": true},
		[]Invocation{{Path: "preferred-model unset", Args: []string{"anthropic"}}}},
	{"tokenops_configure", map[string]any{"setting": "usage_meter", "org": "Acme Inc"},
		[]Invocation{{Path: "vendor-usage setup", Args: []string{"claude-subscription"}, Flags: map[string]any{"org": "Acme Inc"}}}},
	{"tokenops_configure", map[string]any{"setting": "usage_meter"},
		[]Invocation{{Path: "vendor-usage setup", Args: []string{"claude-subscription"}}}},
	{"tokenops_coach", map[string]any{"preset": "autopilot"},
		[]Invocation{{Path: "coach preset", Args: []string{"autopilot"}}}},
	{"tokenops_coach", map[string]any{"autonomy": "advise", "power": "models", "rung": "-ask", "verbosity": "quiet", "holdout": "5"},
		[]Invocation{
			{Path: "coach autonomy", Args: []string{"advise"}},
			{Path: "coach set", Args: []string{"models", "-ask"}},
			{Path: "coach verbosity", Args: []string{"quiet"}},
			{Path: "coach holdout", Args: []string{"5"}},
		}},
	{"tokenops_coach", map[string]any{"tip": "snoozed", "tip_id": "-tip-1", "snooze_minutes": 30},
		[]Invocation{{Path: "coach tip", Args: []string{"snooze"}, Flags: map[string]any{"id": "-tip-1", "for": "30m0s"}}}},
	{"tokenops_coach", map[string]any{"tip": "acknowledged", "tip_id": "tip 2"},
		[]Invocation{{Path: "coach tip", Args: []string{"ack"}, Flags: map[string]any{"id": "tip 2"}}}},
	{"tokenops_coach", map[string]any{"tip": "expected"},
		[]Invocation{{Path: "coach tip", Args: []string{"expected"}}}},
	{"tokenops_experiment", map[string]any{"action": "start", "provider": "anthropic", "baseline_model": "-opus", "variant_model": "claude-sonnet-4",
		"duration_days": 7, "max_pairs": 6, "objective_metric": "plan_quota_tokens", "min_improvement_pct": 12.5, "reason": hostile,
		"guardrails": []any{map[string]any{"metric": "quality", "max_regression_pct": 0}, map[string]any{"metric": "latency_ms", "max_regression_pct": 20}}},
		[]Invocation{{Path: "experiment start", Args: []string{"anthropic", "-opus", "claude-sonnet-4"}, Flags: map[string]any{
			"pairs": "6", "days": "7", "objective": "plan_quota_tokens", "min-improvement-pct": "12.5",
			"guardrail": []string{"quality", "latency_ms:20"}, "reason": hostile}}}},
	{"tokenops_experiment", map[string]any{"action": "stop", "experiment_id": "exp-1", "reason": "-regressed"},
		[]Invocation{{Path: "experiment stop", Args: []string{"exp-1"}, Flags: map[string]any{"reason": "-regressed"}}}},
	{"tokenops_experiment", map[string]any{"action": "stop", "experiment_id": "-exp"},
		[]Invocation{{Path: "experiment stop", Args: []string{"-exp"}, Flags: map[string]any{"reason": ""}}}},
}

// shownCommand is the command a call shows the operator, after the
// arguments have been through JSON as a client's call is, and the route
// the call takes. A route that asks no one shows nothing.
func shownCommand(tool string, args map[string]any) (route, command string, err error) {
	b, err := json.Marshal(args)
	if err != nil {
		return "", "", err
	}
	var in map[string]any
	if err := json.Unmarshal(b, &in); err != nil {
		return "", "", err
	}
	for _, p := range publicTools() {
		if p.name != tool {
			continue
		}
		name, err := p.selected(in)
		if err != nil {
			return "", "", err
		}
		r := p.routes[name]
		if r.change == nil {
			return name, "", nil
		}
		c, err := r.change(in)
		if err != nil || c == nil {
			return name, "", err
		}
		return name, c.command, nil
	}
	return "", "", fmt.Errorf("no public tool %s", tool)
}

// Every route that asks the operator has a case whose command is parsed by
// the terminal's command tree (TestShownCommandsParseToWhatTheToolApplies
// in the external test package).
func TestEveryMutatingRouteHasAnExactCase(t *testing.T) {
	covered := map[string]bool{}
	for _, c := range exactCases {
		route, cmd, err := shownCommand(c.Tool, c.Args)
		if err != nil || cmd == "" {
			t.Errorf("%s %v shows no command (err %v)", c.Tool, c.Args, err)
		}
		covered[c.Tool+"/"+route] = true
	}
	var missing []string
	for _, p := range publicTools() {
		for name, r := range p.routes {
			if r.change != nil && !covered[p.name+"/"+name] {
				missing = append(missing, p.name+"/"+name)
			}
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("routes that ask the operator without an exact case: %v", missing)
	}
}

// No shown command stands in for the real one with a pointer to --help:
// the operator approves, or runs, exactly what the tool would do.
func TestNoShownCommandIsApproximate(t *testing.T) {
	all := make([]ExactCase, 0, len(mutations)+len(moreMutations)+len(exactCases))
	for _, m := range append(append([]mutation{}, mutations...), moreMutations...) {
		all = append(all, ExactCase{Tool: m.tool, Args: m.args})
	}
	all = append(all, exactCases...)
	for _, c := range all {
		_, cmd, err := shownCommand(c.Tool, c.Args)
		if err != nil {
			t.Errorf("%s %v: %v", c.Tool, c.Args, err)
			continue
		}
		for _, approx := range []string{"flags for the other values", "see --help", "--help"} {
			if strings.Contains(cmd, approx) {
				t.Errorf("%s %v shows an approximate command: %q", c.Tool, c.Args, cmd)
			}
		}
	}
}

// A routing rule the tool would refuse is refused before the operator is
// asked to approve it.
func TestAnInvalidRoutingRuleIsRefusedBeforeAsking(t *testing.T) {
	for _, args := range []map[string]any{
		{"action": "set_rule", "provider": "anthropic", "from_model": "claude-opus*", "to_model": "claude-haiku-4"},
		{"action": "set_rule", "provider": "anthropic", "from_model": "claude-opus*", "to_model": "claude-haiku-4", "quality": 1.5},
		{"action": "set_rule", "provider": "anthropic", "from_model": "claude-opus*", "quality": 0.9},
		{"action": "set_rule", "provider": "anthropic", "from_model": "claude-opus*", "to_model": "claude-haiku-4", "quality": 0.9, "fallbacks": []any{1}},
		{"action": "set_rule", "provider": "anthropic", "from_model": "claude-opus*", "to_model": "claude-haiku-4", "quality": 0.9, "fallbacks": []any{""}},
	} {
		srv, calls := spyServer(t)
		op := accepted(true)
		err := execPublic(withOperator(op), t, srv, "tokenops_routing", args)
		if err == nil || len(op.messages) != 0 || len(calls["tokenops_routing_rule_set"]) != 0 {
			t.Errorf("%v: err = %v, asked %d times, applied %d times", args, err, len(op.messages), len(calls["tokenops_routing_rule_set"]))
		}
	}
}

// shellArg makes each value one shell word that the shell reads back as
// the value: no expansion, no splitting, no line break.
func TestShellArgQuotesEveryValueAsOneWord(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", "''"},
		{"claude-opus-4", "claude-opus-4"},
		{"-foo", "-foo"},
		{"two words", "'two words'"},
		{"it's", `'it'\''s'`},
		{`say "hi"`, `'say "hi"'`},
		{"$HOME", "'$HOME'"},
		{"`id`", "'`id`'"},
		{"$(echo x)", "'$(echo x)'"},
		{"a;b|c&d", "'a;b|c&d'"},
		{"glob*", "'glob*'"},
		{"line\nbreak", `$'line\nbreak'`},
		{"tab\there's", `$'tab\there\'s'`},
		{"back\\slash\x01", `$'back\\slash\x01'`},
	}
	for _, c := range cases {
		if got := shellArg(c.in); got != c.want {
			t.Errorf("shellArg(%q) = %s, want %s", c.in, got, c.want)
		}
	}
}
