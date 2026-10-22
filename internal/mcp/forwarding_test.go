package mcp

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"testing"
)

// schemaProperties is the set of property names in a tool's input schema.
func schemaProperties(t *testing.T, schema any) map[string]bool {
	t.Helper()
	raw, err := json.Marshal(schema)
	if err != nil {
		t.Fatal(err)
	}
	var s struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatal(err)
	}
	out := map[string]bool{}
	for k := range s.Properties {
		out[k] = true
	}
	return out
}

// notForwarded lists inner-tool arguments a public route deliberately does
// not pass on, keyed "public/route/argument", with why. Anything else the
// inner tool takes must be forwarded.
var notForwarded = map[string]string{
	"tokenops_sessions/runtime/quote": "the runtime report is derived figures only; tokenops_task_runtime refuses quote",
	"tokenops_status/mode/set":        "tokenops_status only reads; the mode is set through tokenops_configure setting=mode, which asks the operator",
}

// Every argument an inner tool takes reaches it through its public route,
// and every argument a route forwards is one the inner tool takes and the
// public schema lists. A route that drops an argument — so the call is
// refused, or the inner tool silently runs without it — fails here.
func TestPublicRoutesForwardEveryInnerArgument(t *testing.T) {
	srv := fullServer(t)
	for _, p := range publicTools() {
		public := schemaProperties(t, mustSchema(t, p))
		for _, name := range p.routeNames() {
			r := p.routes[name]
			tool, ok := srv.GetTool(r.inner)
			if !ok {
				t.Fatalf("%s: inner tool %s is not registered", p.name, r.inner)
			}
			innerProps := schemaProperties(t, tool.InputSchema())
			forwarded := map[string]bool{}
			for _, k := range r.params {
				if !public[k] {
					t.Errorf("%s/%s forwards %q, which the public schema does not list", p.name, name, k)
				}
				to := k
				if renamed, ok := r.rename[k]; ok {
					to = renamed
				}
				if !innerProps[to] {
					t.Errorf("%s/%s forwards %q, which %s does not take", p.name, name, to, r.inner)
				}
				forwarded[to] = true
			}
			if p.selector != "" {
				// The selector picks the route and is not forwarded; an inner
				// tool that takes it under the same name gets it from the route.
				forwarded[p.selector] = forwarded[p.selector] || !innerProps[p.selector]
			}
			var dropped []string
			for k := range innerProps {
				if forwarded[k] {
					continue
				}
				if _, ok := notForwarded[p.name+"/"+name+"/"+k]; ok {
					continue
				}
				dropped = append(dropped, k)
			}
			sort.Strings(dropped)
			if len(dropped) > 0 {
				t.Errorf("%s/%s does not forward %s to %s", p.name, name, strings.Join(dropped, ", "), r.inner)
			}
		}
	}
	for key := range notForwarded {
		parts := strings.SplitN(key, "/", 3)
		for _, p := range publicTools() {
			if p.name != parts[0] {
				continue
			}
			if r, ok := p.routes[parts[1]]; ok {
				for _, k := range r.params {
					if k == parts[2] {
						t.Errorf("%s is listed as not forwarded but is", key)
					}
				}
			}
		}
	}
}

// mustSchema is a public tool's input schema as the server lists it.
func mustSchema(t *testing.T, p publicTool) any {
	t.Helper()
	srv := NewServer("tokenops", "test", nil)
	inner := map[string]bool{}
	for _, r := range p.routes {
		if inner[r.inner] {
			continue
		}
		inner[r.inner] = true
		srv.Tool(r.inner).SkipValidation().Handler(func(context.Context, map[string]any) (string, error) { return "{}", nil })
	}
	if err := Consolidate(srv); err != nil {
		t.Fatal(err)
	}
	tool, ok := srv.GetTool(p.name)
	if !ok {
		t.Fatalf("no public tool %s", p.name)
	}
	return tool.InputSchema()
}

// startArgs is every argument tokenops_experiment's start takes.
func startArgs() map[string]any {
	return map[string]any{
		"action":              "start",
		"provider":            "anthropic",
		"baseline_model":      "claude-opus-4",
		"variant_model":       "claude-sonnet-4",
		"duration_days":       7,
		"max_pairs":           6,
		"objective_metric":    "plan_quota_tokens",
		"min_improvement_pct": 12.5,
		"guardrails": []any{
			map[string]any{"metric": "quality", "max_regression_pct": 0},
			map[string]any{"metric": "latency_ms", "max_regression_pct": 20},
		},
		"reason": "try sonnet on refactors",
	}
}

// A start through the public tool hands the inner tool every argument, and
// the operator is shown the exact command that makes the same trial.
func TestExperimentStartForwardsEveryArgument(t *testing.T) {
	srv, calls := spyServer(t)
	op := accepted(true)
	if err := execPublic(withOperator(op), t, srv, "tokenops_experiment", startArgs()); err != nil {
		t.Fatalf("start: %v", err)
	}
	got := calls["tokenops_experiment"]
	if len(got) != 1 {
		t.Fatalf("inner calls = %d", len(got))
	}
	for k, want := range startArgs() {
		a, _ := json.Marshal(got[0][k])
		b, _ := json.Marshal(want)
		if string(a) != string(b) {
			t.Errorf("inner %s = %s, want %s", k, a, b)
		}
	}
	wantCmd := "tokenops experiment start anthropic claude-opus-4 claude-sonnet-4 --pairs 6 --days 7 --objective plan_quota_tokens --min-improvement-pct 12.5 --guardrail quality --guardrail latency_ms:20 --reason 'try sonnet on refactors'"
	if len(op.messages) != 1 || !strings.HasSuffix(op.messages[0], "terminal: "+wantCmd) {
		t.Errorf("operator was asked %q, want it to name %q", op.messages, wantCmd)
	}
	for _, s := range []string{`"anthropic"`, `"claude-opus-4"`, `"claude-sonnet-4"`, `"plan_quota_tokens"`, `"try sonnet on refactors"`} {
		if len(op.messages) == 1 && !strings.Contains(op.messages[0], s) {
			t.Errorf("summary does not show %s: %q", s, op.messages[0])
		}
	}
}

// End to end: a confirmed start through the public tool enrolls a trial.
func TestExperimentStartSucceedsThroughThePublicTool(t *testing.T) {
	srv := fullServer(t)
	if err := Consolidate(srv); err != nil {
		t.Fatal(err)
	}
	tool, _ := srv.GetTool("tokenops_experiment")
	b, _ := json.Marshal(startArgs())
	res, err := tool.Execute(withOperator(accepted(true)), b)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	raw, _ := json.Marshal(res)
	var out experimentResult
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	st := out.State
	if st == nil || st.Stage != "started" || st.Variant.Model != "claude-sonnet-4" || st.Baseline.Provider != "anthropic" ||
		st.MaxPairs != 6 || st.ObjectiveMetric != "plan_quota_tokens" || st.Reason != "try sonnet on refactors" {
		t.Fatalf("start result = %s", raw)
	}
}

// A stop's reason reaches the record, and the command shown carries it.
func TestExperimentStopForwardsTheReason(t *testing.T) {
	srv, calls := spyServer(t)
	op := accepted(true)
	if err := execPublic(withOperator(op), t, srv, "tokenops_experiment", map[string]any{"action": "stop", "experiment_id": "exp-1", "reason": "variant regressed"}); err != nil {
		t.Fatal(err)
	}
	if got := calls["tokenops_experiment"]; len(got) != 1 || got[0]["reason"] != "variant regressed" {
		t.Fatalf("inner calls = %v", got)
	}
	if want := "tokenops experiment stop exp-1 --reason 'variant regressed'"; !strings.HasSuffix(op.messages[0], "terminal: "+want) {
		t.Errorf("asked %q, want %q", op.messages[0], want)
	}
}

// The coach's holdout is settable through the public tool, confirmed.
func TestCoachHoldoutForwards(t *testing.T) {
	srv, calls := spyServer(t)
	op := accepted(true)
	if err := execPublic(withOperator(op), t, srv, "tokenops_coach", map[string]any{"holdout": "5"}); err != nil {
		t.Fatal(err)
	}
	if got := calls["tokenops_coach"]; len(got) != 1 || got[0]["holdout"] != "5" {
		t.Fatalf("inner calls = %v", got)
	}
	if want := "tokenops coach holdout 5"; len(op.messages) != 1 || !strings.HasSuffix(op.messages[0], "terminal: "+want) {
		t.Errorf("asked %q, want %q", op.messages, want)
	}
}

// A budget's provider scope, windows and hard mode reach tokenops_budget_set
// through the public tool, and the operator is shown the exact command.
func TestBudgetForwardsEveryArgument(t *testing.T) {
	cases := []struct {
		args  map[string]any
		inner map[string]any
		cmd   string
	}{
		{
			args: map[string]any{"setting": "budget", "name": "mine", "provider": "fireworks", "limit_usd": 50, "window": "daily",
				"add_window": true, "budget_mode": "hard", "require": "deny", "warn_at": 0.6},
			inner: map[string]any{"name": "mine", "provider": "fireworks", "limit_usd": 50.0, "window": "daily",
				"add_window": true, "mode": "hard", "require": "deny", "warn_at": 0.6},
			cmd: "tokenops budget set mine --window daily --limit-usd 50 --warn-at 0.6 --provider fireworks --mode hard --require deny --add-window",
		},
		{
			args:  map[string]any{"setting": "budget", "name": "team", "remove_window": "daily"},
			inner: map[string]any{"name": "team", "remove_window": "daily"},
			cmd:   "tokenops budget unset team --window daily",
		},
	}
	for _, c := range cases {
		srv, calls := spyServer(t)
		op := accepted(true)
		if err := execPublic(withOperator(op), t, srv, "tokenops_configure", c.args); err != nil {
			t.Fatalf("%v: %v", c.args, err)
		}
		got := calls["tokenops_budget_set"]
		if len(got) != 1 {
			t.Fatalf("inner calls = %v", got)
		}
		a, _ := json.Marshal(got[0])
		b, _ := json.Marshal(c.inner)
		if string(a) != string(b) {
			t.Errorf("inner args = %s, want %s", a, b)
		}
		if len(op.messages) != 1 || !strings.HasSuffix(op.messages[0], "terminal: "+c.cmd) {
			t.Errorf("asked %q, want it to name %q", op.messages, c.cmd)
		}
	}
}
