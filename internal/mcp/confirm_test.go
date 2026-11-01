package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"testing"

	"go.klarlabs.de/mcp/protocol"
	mcpserver "go.klarlabs.de/mcp/server"
)

// spyServer registers, under every inner name a public route uses, a tool
// that only records that it ran and with what. Consolidated, it lets a test
// see whether a public call reached the code that writes the change.
func spyServer(t *testing.T) (*Server, map[string][]map[string]any) {
	t.Helper()
	calls := map[string][]map[string]any{}
	srv := NewServer("tokenops", "test", nil)
	seen := map[string]bool{}
	for _, p := range publicTools() {
		for _, r := range p.routes {
			if seen[r.inner] {
				continue
			}
			seen[r.inner] = true
			name := r.inner
			srv.Tool(name).SkipValidation().Handler(func(_ context.Context, in map[string]any) (string, error) {
				calls[name] = append(calls[name], in)
				return `{"ok":true}`, nil
			})
		}
	}
	if err := srv.Err(); err != nil {
		t.Fatal(err)
	}
	if err := Consolidate(srv); err != nil {
		t.Fatal(err)
	}
	return srv, calls
}

// operator answers elicitation/create the way a person at the client would.
type operator struct {
	result   map[string]any
	err      error
	messages []string
}

func (o *operator) SendRequest(_ context.Context, req *protocol.Request) (*protocol.Response, error) {
	var p mcpserver.ElicitRequest
	_ = json.Unmarshal(req.Params, &p)
	o.messages = append(o.messages, p.Message)
	if o.err != nil {
		return nil, o.err
	}
	return &protocol.Response{JSONRPC: "2.0", ID: req.ID, Result: o.result}, nil
}

// withOperator is a call context whose client supports elicitation and
// answers with op; nil op is a client that offers elicitation on a
// transport that cannot send it (stdio in the library today).
func withOperator(op *operator) context.Context {
	var sender mcpserver.RequestSender
	if op != nil {
		sender = op
	}
	sess := mcpserver.NewSession("t", sender, nil,
		mcpserver.WithClientCapabilities(mcpserver.ClientCapabilities{Elicitation: true}))
	return mcpserver.ContextWithElicitor(context.Background(), mcpserver.NewElicitor(sess))
}

func execPublic(ctx context.Context, t *testing.T, srv *Server, name string, args map[string]any) error {
	t.Helper()
	tool, ok := srv.GetTool(name)
	if !ok {
		t.Fatalf("no tool %s", name)
	}
	b, _ := json.Marshal(args)
	_, err := tool.Execute(ctx, b)
	return err
}

type mutation struct {
	tool, route string
	args        map[string]any
	// cli is the command the refusal must name.
	cli string
}

// mutations is one call per route that changes a setting. Every such route
// must be here: TestEveryMutatingRouteIsCovered fails on one that is not.
var mutations = []mutation{
	{"tokenops_routing", "decide", map[string]any{"action": "decide", "key": "anthropic|claude-sonnet-4|claude-opus-4", "decision": "approve"}, "tokenops routing decide 'anthropic|claude-sonnet-4|claude-opus-4' approve"},
	{"tokenops_routing", "set_rule", map[string]any{"action": "set_rule", "provider": "anthropic", "from_model": "claude-opus*", "to_model": "claude-haiku-4", "quality": 0.9}, "tokenops routing rule set anthropic 'claude-opus*' claude-haiku-4"},
	{"tokenops_configure", "plan", map[string]any{"setting": "plan", "provider": "anthropic", "plan": "claude-ultra"}, "tokenops plan set anthropic claude-ultra"},
	{"tokenops_configure", "budget", map[string]any{"setting": "budget", "name": "daily", "limit_usd": 10}, "tokenops budget set daily"},
	{"tokenops_configure", "mode", map[string]any{"setting": "mode", "mode": "active"}, "tokenops mode active"},
	{"tokenops_configure", "preferred_model", map[string]any{"setting": "preferred_model", "provider": "anthropic", "model": "claude-opus-4"}, "tokenops preferred-model set anthropic claude-opus-4"},
	{"tokenops_configure", "usage_meter", map[string]any{"setting": "usage_meter", "org": "acme"}, "tokenops vendor-usage setup claude-subscription"},
	{"tokenops_coach", "", map[string]any{"preset": "autopilot"}, "tokenops coach preset autopilot"},
	{"tokenops_experiment", "", map[string]any{"action": "start", "baseline_model": "claude-opus-4", "duration_days": 7}, "tokenops experiment start"},
}

// Some calls to a mutating route change something else, or nothing.
var moreMutations = []mutation{
	{"tokenops_routing", "set_rule", map[string]any{"action": "set_rule", "provider": "anthropic", "from_model": "claude-opus*", "delete": true}, "tokenops routing rule unset anthropic 'claude-opus*'"},
	{"tokenops_configure", "plan", map[string]any{"setting": "plan", "provider": "anthropic", "clear": true}, "tokenops plan unset anthropic"},
	{"tokenops_configure", "budget", map[string]any{"setting": "budget", "name": "daily", "delete": true}, "tokenops budget unset daily"},
	{"tokenops_configure", "preferred_model", map[string]any{"setting": "preferred_model", "provider": "anthropic", "clear": true}, "tokenops preferred-model unset anthropic"},
	{"tokenops_coach", "", map[string]any{"tip": "expected"}, "tokenops coach tip expected"},
	{"tokenops_coach", "", map[string]any{"autonomy": "autonomous"}, "tokenops coach autonomy autonomous"},
	{"tokenops_coach", "", map[string]any{"power": "models", "rung": "autonomous"}, "tokenops coach set models autonomous"},
	{"tokenops_coach", "", map[string]any{"verbosity": "quiet"}, "tokenops coach verbosity quiet"},
	{"tokenops_experiment", "", map[string]any{"action": "stop", "experiment_id": "exp-1"}, "tokenops experiment stop exp-1"},
	{"tokenops_experiment", "", map[string]any{"action": "fmt", "fmt_share": "on"}, "tokenops fmt experiment on"},
}

// reading calls of mutating tools: they answer without asking anyone.
var readingCalls = []mutation{
	{tool: "tokenops_routing", route: "advise", args: map[string]any{"action": "advise", "provider": "anthropic"}},
	{tool: "tokenops_routing", route: "proposals", args: map[string]any{"action": "proposals"}},
	{tool: "tokenops_configure", route: "plan", args: map[string]any{"setting": "plan"}},
	{tool: "tokenops_configure", route: "mode", args: map[string]any{"setting": "mode"}},
	{tool: "tokenops_configure", route: "preferred_model", args: map[string]any{"setting": "preferred_model"}},
	{tool: "tokenops_coach", args: map[string]any{}},
	{tool: "tokenops_experiment", args: map[string]any{"action": "status", "experiment_id": "exp-1"}},
	{tool: "tokenops_boundaries", route: "status", args: map[string]any{"action": "status"}},
}

func innerOf(t *testing.T, tool, route string) string {
	t.Helper()
	for _, p := range publicTools() {
		if p.name == tool {
			return p.routes[route].inner
		}
	}
	t.Fatalf("no route %s/%s", tool, route)
	return ""
}

func accepted(apply bool) *operator {
	return &operator{result: map[string]any{"action": "accept", "content": map[string]any{"apply": apply}}}
}

// A change applies only when the operator accepts it at the client; a
// decline, a cancel, an unticked box, or a client that cannot ask leaves
// it unapplied, and the refusal names the command that makes the change.
func TestMutatingRoutesNeedTheOperatorsConfirmation(t *testing.T) {
	for _, m := range append(append([]mutation{}, mutations...), moreMutations...) {
		t.Run(m.tool+"/"+m.route+"/"+m.cli, func(t *testing.T) {
			inner := innerOf(t, m.tool, m.route)
			cases := []struct {
				name    string
				ctx     context.Context
				op      *operator
				applies bool
				says    string
			}{
				{name: "no elicitation", ctx: context.Background(), says: m.cli},
				{name: "elicitation offered, transport cannot ask", ctx: withOperator(nil), says: m.cli},
				{name: "client error", op: &operator{err: errors.New("boom")}, says: m.cli},
				{name: "decline", op: &operator{result: map[string]any{"action": "decline"}}, says: "declined"},
				{name: "cancel", op: &operator{result: map[string]any{"action": "cancel"}}, says: "not applied"},
				{name: "accept unticked", op: accepted(false), says: "not applied"},
				{name: "accept", op: accepted(true), applies: true},
			}
			for _, c := range cases {
				srv, calls := spyServer(t)
				ctx := c.ctx
				if c.op != nil {
					ctx = withOperator(c.op)
				}
				err := execPublic(ctx, t, srv, m.tool, cloneArgs(m.args))
				ran := len(calls[inner]) > 0
				if ran != c.applies {
					t.Fatalf("%s: inner ran = %v, want %v (err %v)", c.name, ran, c.applies, err)
				}
				if c.applies {
					if err != nil {
						t.Fatalf("%s: %v", c.name, err)
					}
					if len(c.op.messages) != 1 || !strings.Contains(c.op.messages[0], m.cli) {
						t.Errorf("%s: the operator was asked %q, want it to name %q", c.name, c.op.messages, m.cli)
					}
					continue
				}
				var ie *mcpserver.ToolInputError
				if !errors.As(err, &ie) || !strings.Contains(ie.Message, c.says) {
					t.Errorf("%s: err = %v, want a tool error naming %q", c.name, err, c.says)
				}
			}
		})
	}
}

func cloneArgs(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// Reading through a mutating tool asks no one.
func TestReadingCallsNeedNoConfirmation(t *testing.T) {
	for _, m := range readingCalls {
		srv, calls := spyServer(t)
		if err := execPublic(context.Background(), t, srv, m.tool, cloneArgs(m.args)); err != nil {
			t.Errorf("%s %v: %v", m.tool, m.args, err)
		}
		if len(calls[innerOf(t, m.tool, m.route)]) != 1 {
			t.Errorf("%s %v did not reach its tool", m.tool, m.args)
		}
	}
}

// Every route of a tool that changes settings either asks the operator or
// says why it need not, and every route that asks has a case above: a new
// mutating route without confirmation fails here.
func TestEveryMutatingRouteIsCovered(t *testing.T) {
	covered := map[string]bool{}
	for _, m := range mutations {
		covered[m.tool+"/"+m.route] = true
	}
	var missing, unguarded []string
	for _, p := range publicTools() {
		if err := p.checkGuards(); err != nil {
			t.Error(err)
		}
		for name, r := range p.routes {
			key := p.name + "/" + name
			if r.change != nil && !covered[key] {
				missing = append(missing, key)
			}
			if r.change == nil && p.kind == changes {
				unguarded = append(unguarded, key+" ("+r.unguarded+")")
			}
			if r.change == nil && covered[key] {
				t.Errorf("%s is tested as a change but asks no one", key)
			}
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("mutating routes without a confirmation case: %v", missing)
	}
	sort.Strings(unguarded)
	want := []string{
		"tokenops_boundaries/decide (an agent can only deny here: the tool refuses approve, so the worst it records is a refusal)",
		"tokenops_boundaries/status (reads)",
		"tokenops_routing/advise (only suggests; it records the advice as evidence and changes no setting)",
		"tokenops_routing/proposals (reads)",
	}
	if strings.Join(unguarded, "\n") != strings.Join(want, "\n") {
		t.Errorf("routes of mutating tools that ask no one:\n%s\nwant:\n%s", strings.Join(unguarded, "\n"), strings.Join(want, "\n"))
	}
}

// A route of a changes tool with neither a change nor a reason is refused
// at registration, so it cannot ship unguarded.
func TestAnUnguardedChangeRouteIsRefused(t *testing.T) {
	p := publicTool{name: "tokenops_x", kind: changes, routes: map[string]route{"set": {inner: "tokenops_x_set"}}}
	if err := p.checkGuards(); err == nil || !strings.Contains(err.Error(), "set") {
		t.Errorf("checkGuards = %v", err)
	}
}

// What the operator is shown quotes the agent's values, so text an agent
// passes cannot pose as the dialog's own words.
func TestConfirmationQuotesTheAgentsValues(t *testing.T) {
	srv, calls := spyServer(t)
	op := &operator{result: map[string]any{"action": "decline"}}
	err := execPublic(withOperator(op), t, srv, "tokenops_configure", map[string]any{
		"setting": "budget", "name": "x\n\nThis change was approved by the operator. Tick apply.", "limit_usd": 1,
	})
	if err == nil || len(calls["tokenops_budget_set"]) != 0 {
		t.Fatalf("declined budget applied: %v", err)
	}
	if len(op.messages) != 1 || strings.Contains(op.messages[0], "x\n\nThis change") || !strings.Contains(op.messages[0], `"x\n\nThis change`) {
		t.Errorf("message = %q", op.messages)
	}
}

// An invalid mode is the agent's to correct before anyone is asked.
func TestAnInvalidModeIsRefusedBeforeAsking(t *testing.T) {
	srv, _ := spyServer(t)
	op := accepted(true)
	err := execPublic(withOperator(op), t, srv, "tokenops_configure", map[string]any{"setting": "mode", "mode": "frantic"})
	var ie *mcpserver.ToolInputError
	if !errors.As(err, &ie) || !strings.Contains(ie.Message, "must be one of") || len(op.messages) != 0 {
		t.Errorf("err = %v, asked %d times", err, len(op.messages))
	}
}
