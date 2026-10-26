package mcp

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"

	mcpgo "go.klarlabs.de/mcp"
	mcpserver "go.klarlabs.de/mcp/server"

	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/pkg/eventschema"
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

// argRaw is a string argument as the inner tool receives it, untrimmed, for
// a command that must match what the tool applies.
func argRaw(args map[string]any, k string) string {
	s, _ := args[k].(string)
	return s
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

// cli builds a tokenops command line: the command's path, its positional
// words and its flags, quoting the agent's values. A word that starts with
// a dash would be read as a flag if pasted, so when one does the flags go
// first and -- ends them: everything after it is a positional word.
func cli(path string, args []string, flags ...string) string {
	parts := make([]string, 0, 2+len(args)+len(flags))
	parts = append(parts, "tokenops "+path)
	dashed := false
	for _, a := range args {
		dashed = dashed || strings.HasPrefix(a, "-")
	}
	if dashed {
		parts = append(parts, flags...)
		parts = append(parts, "--")
	}
	for _, a := range args {
		parts = append(parts, shellArg(a))
	}
	if !dashed {
		parts = append(parts, flags...)
	}
	return strings.Join(parts, " ")
}

// words is its arguments as a slice, for cli.
func words(w ...string) []string { return w }

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

func routingDecideChange(args map[string]any) (*change, error) {
	key, decision := argString(args, "key"), argString(args, "decision")
	summary := fmt.Sprintf("Approve the routing proposal %s: requests that match it will run on the proposed, pricier model.", quoted(key))
	if decision != "approve" {
		summary = fmt.Sprintf("Deny the routing proposal %s: matching requests stay on the model they asked for.", quoted(key))
	}
	// The tool and the terminal both hand the key and decision to the
	// same decision, untrimmed.
	return &change{summary: summary, command: cli("routing decide", words(argRaw(args, "key"), argRaw(args, "decision")))}, nil
}

func routingRuleChange(args map[string]any) (*change, error) {
	// The tool and the terminal store the rule's values as given, so the
	// command carries them untrimmed.
	provider, from := argRaw(args, "provider"), argRaw(args, "from_model")
	if argBool(args, "delete") {
		return &change{
			summary: fmt.Sprintf("Remove the routing rule for %s requests to %s.", quoted(provider), quoted(from)),
			command: cli("routing rule unset", words(provider, from)),
		}, nil
	}
	to := argRaw(args, "to_model")
	quality, _ := args["quality"].(float64)
	fallbacks, err := stringList(args, "fallbacks")
	if err != nil {
		return nil, err
	}
	// Refused before anyone is asked, with the check the tool and the
	// terminal run: an operator is not asked to approve a rule that would
	// not be written.
	for _, f := range fallbacks {
		if f == "" {
			// --fallback '' sets no fallback at all, so the command could
			// not say what the tool would store.
			return nil, &mcpgo.ToolInputError{Message: "routing rule: a fallback must not be empty"}
		}
	}
	rule := config.RoutingRuleConfig{Provider: provider, FromModel: from, ToModel: to, Quality: quality, Fallbacks: fallbacks}
	if err := rule.Validate(); err != nil {
		return nil, &mcpgo.ToolInputError{Message: "routing rule: " + err.Error()}
	}
	fl := flagWords(args, cliFlag{"quality", "--quality"})
	for _, f := range fallbacks {
		// --fallback reads its value as comma-separated values, so a
		// fallback with a comma or a quote is written as one CSV field.
		fl = append(fl, flagValue("--fallback", csvField(f)))
	}
	return &change{
		summary: fmt.Sprintf("Route %s requests for %s to %s%s. In active mode the proxy rewrites matching live requests.",
			quoted(provider), quoted(from), quoted(to), details(args, "quality", "fallbacks")),
		command: cli("routing rule set", words(provider, from, to), fl...),
	}, nil
}

// stringList is a list-of-strings argument; anything else in it is the
// agent's to correct.
func stringList(args map[string]any, k string) ([]string, error) {
	raw, ok := args[k]
	if !ok || raw == nil {
		return nil, nil
	}
	items, ok := raw.([]any)
	if !ok {
		return nil, &mcpgo.ToolInputError{Message: k + " must be a list of strings"}
	}
	out := make([]string, 0, len(items))
	for _, it := range items {
		s, ok := it.(string)
		if !ok {
			return nil, &mcpgo.ToolInputError{Message: k + " must be a list of strings"}
		}
		out = append(out, s)
	}
	return out, nil
}

// csvField is v as one field of a comma-separated list, the way a string
// slice flag reads it.
func csvField(v string) string {
	var b strings.Builder
	w := csv.NewWriter(&b)
	_ = w.Write([]string{v})
	w.Flush()
	return strings.TrimSuffix(strings.TrimSuffix(b.String(), "\n"), "\r")
}

// planFlags are plan set's flags for the plan route's extras.
var planFlags = []cliFlag{
	{"spend_limit_usd", "--spend-limit"}, {"limit_window", "--limit-window"}, {"rate_factor", "--rate-factor"},
	{"price", "--price"}, {"currency", "--currency"}, {"since", "--since"},
}

func planChange(args map[string]any) (*change, error) {
	// The tool trims the provider and plan before binding; the terminal
	// binds them as given, so the command carries them trimmed.
	provider := argString(args, "provider")
	if provider == "" {
		return nil, nil // lists the bindings
	}
	if argBool(args, "clear") {
		return &change{summary: fmt.Sprintf("Remove the plan bound to %s.", quoted(provider)), command: cli("plan unset", words(provider))}, nil
	}
	plan := argString(args, "plan")
	extra := details(args, "spend_limit_usd", "limit_window", "rate_factor", "price", "currency", "since")
	summary := fmt.Sprintf("Bind %s to the plan %s%s.", quoted(provider), quoted(plan), extra)
	if argString(args, "since") != "" {
		summary += " since re-marks usage recorded as billed since that date as plan-covered."
	}
	return &change{summary: summary, command: cli("plan set", words(provider, plan), flagWords(args, planFlags...)...)}, nil
}

func budgetChange(args map[string]any) (*change, error) {
	name := argString(args, "name")
	if argBool(args, "delete") {
		return &change{summary: fmt.Sprintf("Delete the budget %s.", quoted(name)), command: cli("budget unset", words(argRaw(args, "name")))}, nil
	}
	if w := argString(args, "remove_window"); w != "" {
		return &change{
			summary: fmt.Sprintf("Remove the %s window of the budget %s.", quoted(w), quoted(name)),
			command: cli("budget unset", words(argRaw(args, "name")), flagWords(args, cliFlag{"remove_window", "--window"})...),
		}, nil
	}
	summary := fmt.Sprintf("Set the budget %s%s.", quoted(name),
		details(args, "window", "limit_usd", "limit_tokens", "warn_at", "crit_at", "workflow_id", "agent_id", "basis", "tag", "provider", "budget_mode", "require", "add_window"))
	if strings.EqualFold(argString(args, "budget_mode"), "hard") {
		summary += " A hard window is a spend boundary; it starts in shadow, and once it holds it can be changed only from a terminal."
	}
	return &change{summary: summary, command: cli("budget set", words(argRaw(args, "name")), flagWords(args, budgetFlags...)...)}, nil
}

// cliFlag maps a public argument to the terminal flag that sets the same
// value.
type cliFlag struct{ arg, name string }

// budgetFlags are budget set's flags, in the order the command shows them.
var budgetFlags = []cliFlag{
	{"window", "--window"}, {"limit_usd", "--limit-usd"}, {"limit_tokens", "--limit-tokens"},
	{"warn_at", "--warn-at"}, {"crit_at", "--crit-at"}, {"basis", "--basis"},
	{"workflow_id", "--workflow-id"}, {"agent_id", "--agent-id"}, {"tag", "--tag"},
	{"provider", "--provider"}, {"budget_mode", "--mode"}, {"require", "--require"},
	{"add_window", "--add-window"},
}

// flagWords writes the passed arguments as terminal flags: a true bool as
// the bare flag, a number in its shortest exact form, anything else as one
// quoted shell word, as passed: the tool gets it untrimmed too. Arguments
// left out or zero are left out, as the terminal's own defaults then apply
// to both. A value is joined to its flag with =, so one that starts with a
// dash cannot be read as a flag of its own.
func flagWords(args map[string]any, fs ...cliFlag) []string {
	var out []string
	for _, f := range fs {
		switch v := args[f.arg].(type) {
		case bool:
			if v {
				out = append(out, f.name)
			}
		case float64:
			if v != 0 {
				out = append(out, flagValue(f.name, strconv.FormatFloat(v, 'f', -1, 64)))
			}
		case string:
			if v != "" {
				out = append(out, flagValue(f.name, v))
			}
		}
	}
	return out
}

// flagValue is one flag with its value: --name value, or --name=value when
// the value starts with a dash.
func flagValue(name, v string) string {
	if strings.HasPrefix(v, "-") {
		return name + "=" + shellArg(v)
	}
	return name + " " + shellArg(v)
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
	return &change{summary: summary, command: cli("mode", words(mode))}, nil
}

func preferredModelChange(args map[string]any) (*change, error) {
	provider := argString(args, "provider")
	if provider == "" {
		return nil, nil // lists the ceilings
	}
	if argBool(args, "clear") {
		return &change{
			summary: fmt.Sprintf("Remove the preferred-model ceiling for %s: routing to pricier models is no longer referred to you.", quoted(provider)),
			command: cli("preferred-model unset", words(provider)),
		}, nil
	}
	model := argString(args, "model")
	return &change{
		summary: fmt.Sprintf("Make %s the most expensive model %s may be routed to.", quoted(model), quoted(provider)),
		command: cli("preferred-model set", words(provider, model)),
	}, nil
}

func usageMeterChange(args map[string]any) (*change, error) {
	return &change{
		summary: fmt.Sprintf("Connect claude.ai's usage meter%s with the session key in the environment or config, and turn it on in config.yaml.", details(args, "org")),
		command: cli("vendor-usage setup", words("claude-subscription"), flagWords(args, cliFlag{"org", "--org"})...),
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
		// The tool looks the id up as given, as --id does; a snooze of
		// zero or less takes the default on both.
		fl := flagWords(args, cliFlag{"tip_id", "--id"})
		if m, _ := args["snooze_minutes"].(float64); m != 0 {
			fl = append(fl, flagValue("--for", strconv.FormatFloat(m, 'f', -1, 64)+"m"))
		}
		return &change{
			summary: fmt.Sprintf("Answer the coach's tip%s as %s.", details(args, "tip_id", "snooze_minutes"), quoted(tip)),
			command: cli("coach tip", words(word), fl...),
		}, nil
	}
	if preset := argString(args, "preset"); preset != "" {
		return &change{
			summary: fmt.Sprintf("Set the coach preset %s: it sets every power's autonomy and installs the hooks it needs on every installed agent.", quoted(preset)),
			command: cli("coach preset", words(preset)),
		}, nil
	}
	var parts, cmds []string
	if a := argString(args, "autonomy"); a != "" {
		parts = append(parts, "every power's autonomy to "+quoted(a))
		cmds = append(cmds, cli("coach autonomy", words(a)))
	}
	if power, rung := argString(args, "power"), argString(args, "rung"); power != "" || rung != "" {
		parts = append(parts, fmt.Sprintf("the %s power's autonomy to %s", quoted(power), quoted(rung)))
		cmds = append(cmds, cli("coach set", words(power, rung)))
	}
	if v := argString(args, "verbosity"); v != "" {
		parts = append(parts, "the coach's verbosity to "+quoted(v))
		cmds = append(cmds, cli("coach verbosity", words(v)))
	}
	if h := argString(args, "holdout"); h != "" {
		parts = append(parts, "the share of sessions whose cost and drift tips are held back to "+quoted(h))
		cmds = append(cmds, cli("coach holdout", words(argRaw(args, "holdout"))))
	}
	if h := argString(args, "headroom_note"); h != "" {
		parts = append(parts, "the plan headroom note "+quoted(h))
		cmds = append(cmds, cli("coach headroom-note", words(h)))
	}
	if len(parts) == 0 {
		return nil, nil // reports the settings
	}
	return &change{summary: "Set " + strings.Join(parts, ", and ") + ".", command: strings.Join(cmds, " && ")}, nil
}

// experimentStartFlags are experiment start's flags, in the order the
// command shows them.
var experimentStartFlags = []cliFlag{
	{"max_pairs", "--pairs"}, {"duration_days", "--days"}, {"objective_metric", "--objective"},
	{"min_improvement_pct", "--min-improvement-pct"},
}

func experimentChange(args map[string]any) (*change, error) {
	switch strings.ToLower(argString(args, "action")) {
	case "start":
		provider, baseline, variant := argRaw(args, "provider"), argRaw(args, "baseline_model"), argRaw(args, "variant_model")
		guardrails, err := experimentGuardrails(args)
		if err != nil {
			return nil, err
		}
		fl := flagWords(args, experimentStartFlags...)
		shown := map[string]any{}
		for k, v := range args {
			shown[k] = v
		}
		delete(shown, "guardrails")
		for _, g := range guardrails {
			fl = append(fl, flagValue("--guardrail", g))
		}
		if len(guardrails) > 0 {
			shown["guardrails"] = strings.Join(guardrails, " ")
		}
		// The tool trims the reason it records, as stop does.
		if r := argString(args, "reason"); r != "" {
			fl = append(fl, flagValue("--reason", r))
		}
		extra := details(shown, "duration_days", "max_pairs", "objective_metric", "min_improvement_pct", "guardrails", "reason")
		return &change{
			summary: fmt.Sprintf("Start a routing experiment on %s traffic: %s against the variant %s%s. Part of the matching live traffic runs on the variant until it ends.",
				quoted(provider), quoted(baseline), quoted(variant), extra),
			command: cli("experiment start", words(provider, baseline, variant), fl...),
		}, nil
	case "stop":
		id, reason := argRaw(args, "experiment_id"), argString(args, "reason")
		// The terminal's --reason has a default the tool does not, so the
		// command always names the reason the tool will record.
		return &change{
			summary: fmt.Sprintf("Stop the routing experiment %s%s; its evidence is kept.", quoted(id), details(args, "reason")),
			command: cli("experiment stop", words(id), flagValue("--reason", reason)),
		}, nil
	}
	return nil, nil // status
}

// experimentGuardrails is each guardrail as the terminal's --guardrail
// takes it: quality, or metric:max-regression-pct.
func experimentGuardrails(args map[string]any) ([]string, error) {
	raw, ok := args["guardrails"]
	if !ok || raw == nil {
		return nil, nil
	}
	b, err := json.Marshal(raw)
	if err != nil {
		return nil, err
	}
	var gs []eventschema.ExperimentGuardrail
	if err := json.Unmarshal(b, &gs); err != nil {
		return nil, &mcpgo.ToolInputError{Message: "guardrails must be a list of {metric, max_regression_pct}: " + err.Error()}
	}
	out := make([]string, 0, len(gs))
	for _, g := range gs {
		if g.Metric == "quality" && g.MaxRegressionPct == 0 {
			out = append(out, "quality")
			continue
		}
		out = append(out, g.Metric+":"+strconv.FormatFloat(g.MaxRegressionPct, 'f', -1, 64))
	}
	return out, nil
}
