package mcp

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"

	mcpgo "go.klarlabs.de/mcp"
	mcpserver "go.klarlabs.de/mcp/server"
)

// The tools that change settings used to carry only a destructive hint, so
// the host's permission prompt was the only thing between an agent and a
// new operating mode, routing rule or approved model upgrade. An operator
// who allowlisted mcp__tokenops__* had none: text an agent read — a quoted
// transcript, a web page, a file — could ask it to switch TokenOps to
// active mode and it would. Every call that changes a setting now asks the
// operator through MCP elicitation, at the client and outside the agent's
// turn, and applies only on their yes. A client that cannot ask gets a
// refusal that names the terminal command that makes the same change.

// change is what one call would change.
type change struct {
	// summary says what changes, for the operator. Values the agent passed
	// are quoted so they cannot pose as the dialog's own words.
	summary string
	// command is the tokenops command that makes the same change from a
	// terminal.
	command string
}

// checkGuards refuses a route of a settings-changing tool that neither asks
// the operator nor says why it need not.
func (p publicTool) checkGuards() error {
	if p.kind != changes {
		return nil
	}
	var bad []string
	for name, r := range p.routes {
		if (r.change == nil) == (r.unguarded == "") {
			bad = append(bad, name)
		}
	}
	if len(bad) == 0 {
		return nil
	}
	sort.Strings(bad)
	return fmt.Errorf("%s: routes %s must either ask the operator (change) or say why they need not (unguarded), not both or neither", p.name, strings.Join(bad, ", "))
}

// confirmSchema is the form the operator answers: one box to tick. An
// accept without it ticked applies nothing.
var confirmSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"apply": map[string]any{
			"type":        "boolean",
			"title":       "Apply this change",
			"description": "Tick only if you asked for this change yourself.",
			"default":     false,
		},
	},
	"required": []string{"apply"},
}

// confirm asks the operator to approve c and returns nil only on their yes.
// Every other outcome is a tool error that says nothing was changed.
func confirm(ctx context.Context, c change) error {
	el := mcpserver.ElicitFromContext(ctx)
	if el == nil {
		return refused(c, "this MCP client cannot ask you to confirm a change (it does not offer elicitation)")
	}
	res, err := el.Elicit(ctx, &mcpserver.ElicitRequest{
		Message: "An agent asks to change TokenOps:\n\n" + c.summary +
			"\n\nApply it only if you asked for this. The same change from a terminal: " + c.command,
		RequestedSchema: confirmSchema,
	})
	if errors.Is(err, mcpserver.ErrInputRequired) {
		// A stateless (2026-07-28) client answers by retrying the call with
		// the operator's response; the library turns this into that request.
		return err
	}
	if err != nil {
		reason := "the confirmation could not be shown"
		if errors.Is(err, mcpserver.ErrNoRequestSender) {
			reason = "this connection cannot ask you to confirm a change (the transport does not carry server requests)"
		}
		return refused(c, reason)
	}
	switch res.Action {
	case "accept":
		if apply, _ := res.Content["apply"].(bool); apply {
			return nil
		}
		return notApplied(c, "the operator accepted without ticking apply")
	case "decline":
		return notApplied(c, "the operator declined")
	default:
		return notApplied(c, "the operator dismissed the confirmation")
	}
}

func refused(c change, reason string) error {
	return &mcpgo.ToolInputError{Message: fmt.Sprintf(
		"not applied: %s. A change to TokenOps' settings from an agent needs the operator's confirmation. Nothing was changed. Ask the operator to run it in their own terminal: %s",
		reason, c.command)}
}

func notApplied(c change, why string) error {
	return &mcpgo.ToolInputError{Message: fmt.Sprintf(
		"not applied: %s. Nothing was changed; do not retry unless the operator asks. (%s)", why, c.summary)}
}

// --- what each route changes ----------------------------------------------

func argString(args map[string]any, k string) string {
	s, _ := args[k].(string)
	return strings.TrimSpace(s)
}

func argBool(args map[string]any, k string) bool {
	b, _ := args[k].(bool)
	return b
}

// quoted is an agent-supplied value as the operator sees it: quoted,
// escaped and bounded.
func quoted(v any) string {
	s := fmt.Sprint(v)
	if r := []rune(s); len(r) > 80 {
		s = string(r[:80]) + "…"
	}
	return strconv.Quote(s)
}

var shellSafe = regexp.MustCompile(`^[A-Za-z0-9._:/@=+,-]+$`)

// shellArg is v as one shell word. A value with a control character is
// written in ANSI-C quotes, so a newline cannot break the line it is shown
// on.
func shellArg(v string) string {
	if v == "" {
		return "''"
	}
	if shellSafe.MatchString(v) {
		return v
	}
	if strings.IndexFunc(v, unicode.IsControl) < 0 {
		return "'" + strings.ReplaceAll(v, "'", `'\''`) + "'"
	}
	var b strings.Builder
	b.WriteString("$'")
	for _, r := range v {
		switch {
		case r == '\\' || r == '\'':
			b.WriteRune('\\')
			b.WriteRune(r)
		case r == '\n':
			b.WriteString(`\n`)
		case r == '\t':
			b.WriteString(`\t`)
		case unicode.IsControl(r):
			fmt.Fprintf(&b, `\x%02x`, r)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteString("'")
	return b.String()
}

// cli builds a tokenops command line from words, quoting the agent's.
func cli(fixed string, words ...string) string {
	parts := make([]string, 0, 1+len(words))
	parts = append(parts, "tokenops "+fixed)
	for _, w := range words {
		parts = append(parts, shellArg(w))
	}
	return strings.Join(parts, " ")
}

// details lists the given arguments that were passed, quoted.
func details(args map[string]any, keys ...string) string {
	var out []string
	for _, k := range keys {
		v, ok := args[k]
		if !ok || v == nil || v == "" || v == false || v == 0.0 {
			continue
		}
		out = append(out, k+"="+quoted(v))
	}
	if len(out) == 0 {
		return ""
	}
	return " (" + strings.Join(out, ", ") + ")"
}

const moreFlags = ", with flags for the other values (see --help)"

func routingDecideChange(args map[string]any) (*change, error) {
	key, decision := argString(args, "key"), argString(args, "decision")
	summary := fmt.Sprintf("Approve the routing proposal %s: requests that match it will run on the proposed, pricier model.", quoted(key))
	if decision != "approve" {
		summary = fmt.Sprintf("Deny the routing proposal %s: matching requests stay on the model they asked for.", quoted(key))
	}
	return &change{summary: summary, command: cli("routing decide", key, decision)}, nil
}

func routingRuleChange(args map[string]any) (*change, error) {
	provider, from := argString(args, "provider"), argString(args, "from_model")
	if argBool(args, "delete") {
		return &change{
			summary: fmt.Sprintf("Remove the routing rule for %s requests to %s.", quoted(provider), quoted(from)),
			command: cli("routing rule unset", provider, from),
		}, nil
	}
	to := argString(args, "to_model")
	cmd := cli("routing rule set", provider, from, to)
	if _, ok := args["quality"]; ok || args["fallbacks"] != nil {
		cmd += moreFlags
	}
	return &change{
		summary: fmt.Sprintf("Route %s requests for %s to %s%s. In active mode the proxy rewrites matching live requests.",
			quoted(provider), quoted(from), quoted(to), details(args, "quality", "fallbacks")),
		command: cmd,
	}, nil
}

func planChange(args map[string]any) (*change, error) {
	provider := argString(args, "provider")
	if provider == "" {
		return nil, nil // lists the bindings
	}
	if argBool(args, "clear") {
		return &change{summary: fmt.Sprintf("Remove the plan bound to %s.", quoted(provider)), command: cli("plan unset", provider)}, nil
	}
	plan := argString(args, "plan")
	extra := details(args, "spend_limit_usd", "limit_window", "rate_factor", "price", "currency", "since")
	cmd := cli("plan set", provider, plan)
	if extra != "" {
		cmd += moreFlags
	}
	summary := fmt.Sprintf("Bind %s to the plan %s%s.", quoted(provider), quoted(plan), extra)
	if argString(args, "since") != "" {
		summary += " since re-marks usage recorded as billed since that date as plan-covered."
	}
	return &change{summary: summary, command: cmd}, nil
}

func budgetChange(args map[string]any) (*change, error) {
	name := argString(args, "name")
	if argBool(args, "delete") {
		return &change{summary: fmt.Sprintf("Delete the budget %s.", quoted(name)), command: cli("budget unset", name)}, nil
	}
	return &change{
		summary: fmt.Sprintf("Set the budget %s%s.", quoted(name),
			details(args, "window", "limit_usd", "limit_tokens", "warn_at", "crit_at", "workflow_id", "agent_id", "basis", "tag")),
		command: cli("budget set", name) + moreFlags,
	}, nil
}

func modeChange(args map[string]any) (*change, error) {
	mode := argString(args, "mode")
	if mode == "" {
		return nil, nil // reads the mode
	}
	summary := "Switch TokenOps to passive mode: it stops applying routing rules to live traffic and stops the budget watcher."
	if mode == "active" {
		summary = "Switch TokenOps to active mode: routing rules are applied to live proxied traffic and a budget watcher runs; a daemon is started if none runs."
	}
	return &change{summary: summary, command: cli("mode", mode)}, nil
}

func preferredModelChange(args map[string]any) (*change, error) {
	provider := argString(args, "provider")
	if provider == "" {
		return nil, nil // lists the ceilings
	}
	if argBool(args, "clear") {
		return &change{
			summary: fmt.Sprintf("Remove the preferred-model ceiling for %s: routing to pricier models is no longer referred to you.", quoted(provider)),
			command: cli("preferred-model unset", provider),
		}, nil
	}
	model := argString(args, "model")
	return &change{
		summary: fmt.Sprintf("Make %s the most expensive model %s may be routed to.", quoted(model), quoted(provider)),
		command: cli("preferred-model set", provider, model),
	}, nil
}

func usageMeterChange(args map[string]any) (*change, error) {
	return &change{
		summary: fmt.Sprintf("Connect claude.ai's usage meter%s with the session key in the environment or config, and turn it on in config.yaml.", details(args, "org")),
		command: "tokenops vendor-usage setup claude-subscription",
	}, nil
}

// tipWords maps the tool's tip answers to the terminal's.
var tipWords = map[string]string{"acknowledged": "ack", "expected": "expected", "snoozed": "snooze"}

func coachChange(args map[string]any) (*change, error) {
	if tip := argString(args, "tip"); tip != "" {
		word := tipWords[tip]
		if word == "" {
			word = tip
		}
		cmd := cli("coach tip", word)
		if argString(args, "tip_id") != "" || args["snooze_minutes"] != nil {
			cmd += moreFlags
		}
		return &change{
			summary: fmt.Sprintf("Answer the coach's tip%s as %s.", details(args, "tip_id", "snooze_minutes"), quoted(tip)),
			command: cmd,
		}, nil
	}
	if preset := argString(args, "preset"); preset != "" {
		return &change{
			summary: fmt.Sprintf("Set the coach preset %s: it sets every power's autonomy and installs the hooks it needs on every installed agent.", quoted(preset)),
			command: cli("coach preset", preset),
		}, nil
	}
	var parts, cmds []string
	if a := argString(args, "autonomy"); a != "" {
		parts = append(parts, "every power's autonomy to "+quoted(a))
		cmds = append(cmds, cli("coach autonomy", a))
	}
	if power, rung := argString(args, "power"), argString(args, "rung"); power != "" || rung != "" {
		parts = append(parts, fmt.Sprintf("the %s power's autonomy to %s", quoted(power), quoted(rung)))
		cmds = append(cmds, cli("coach set", power, rung))
	}
	if v := argString(args, "verbosity"); v != "" {
		parts = append(parts, "the coach's verbosity to "+quoted(v))
		cmds = append(cmds, cli("coach verbosity", v))
	}
	if len(parts) == 0 {
		return nil, nil // reports the settings
	}
	return &change{summary: "Set " + strings.Join(parts, ", and ") + ".", command: strings.Join(cmds, " && ")}, nil
}

func experimentChange(args map[string]any) (*change, error) {
	switch argString(args, "action") {
	case "start":
		return &change{
			summary: fmt.Sprintf("Start a routing experiment%s: part of the matching live traffic runs on a variant model until it ends.",
				details(args, "baseline_model", "duration_days", "max_pairs", "min_improvement_pct", "guardrails")),
			command: "tokenops experiment start <provider> <baseline-model> <variant-model>" + moreFlags,
		}, nil
	case "stop":
		id := argString(args, "experiment_id")
		return &change{summary: fmt.Sprintf("Stop the routing experiment %s; its evidence is kept.", quoted(id)), command: cli("experiment stop", id)}, nil
	}
	return nil, nil // status
}
