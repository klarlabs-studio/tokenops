// Package boundaries decides, before an agent acts, whether the action is
// within the limits its owner set, and who may let it go further
// (ADR 0014).
//
// A boundary is a rule: an action the agent is about to take, the figures
// that must hold for the rule to apply, and what is then required — allow,
// ask, a named approver, or deny. The requirement maps onto the one
// authority ladder (internal/contexts/policy), and rules combine the way
// policy.Effective combines rungs: the strictest matching rule wins. A
// boundary only ever tightens.
//
// Conditions read figures TokenOps already holds — session cost, plan
// headroom, model tier, workflow and agent — and never prompt text. The
// package is pure: callers supply the figures, and nothing here reads a
// file or a store, so the evaluation that runs before every tool call
// stays in memory.
//
// Every rule starts in shadow (ADR 0004: explain before automating). A
// shadow rule is evaluated and recorded as "would have asked/refused", and
// binds nothing until it is promoted.
package boundaries

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"go.klarlabs.de/tokenops/internal/contexts/policy"
)

// Action is what the agent is about to do. Only resource actions: what
// tools an agent may run is the harness's business, not TokenOps'.
type Action string

// The actions. A rule naming anything else is refused rather than
// silently ignored.
const (
	// ActionContinue is the agent's next tool call.
	ActionContinue Action = "continue"
	// ActionSubagentModel is the model a subagent is about to run on.
	ActionSubagentModel Action = "subagent_model"
	// ActionSpend is the agent's next tool call, judged by money: the
	// configured budgets and the session's cost.
	ActionSpend Action = "spend"
	// ActionModelRoute is the router moving a request to another model.
	ActionModelRoute Action = "model_route"
	// ActionShare is figures leaving the machine: a team upload or an
	// OTel export.
	ActionShare Action = "share"
)

var actions = []Action{ActionContinue, ActionSubagentModel, ActionSpend, ActionModelRoute, ActionShare}

// Field is a figure a condition reads.
type Field string

// The figures.
const (
	FieldSessionCost Field = "session_cost_usd"
	FieldPlanLeft    Field = "plan_left_pct"
	FieldBudgetUsed  Field = "budget_used_pct"
	FieldModelTier   Field = "model_tier"
	FieldDestination Field = "destination"
	FieldWorkflow    Field = "workflow_id"
	FieldAgent       Field = "agent_id"
)

// fieldOrder fixes the order conditions are checked and reported in, so
// an error or a reason reads the same on every run.
var fieldOrder = []Field{FieldSessionCost, FieldPlanLeft, FieldBudgetUsed, FieldModelTier, FieldDestination, FieldWorkflow, FieldAgent}

// fieldsFor are the figures each action can be judged by: the ones known
// where the action is decided. A condition the decision point cannot read
// is refused at load rather than never matching in silence.
var fieldsFor = map[Action][]Field{
	ActionContinue:      {FieldSessionCost, FieldPlanLeft, FieldWorkflow, FieldAgent},
	ActionSubagentModel: {FieldSessionCost, FieldPlanLeft, FieldModelTier, FieldWorkflow, FieldAgent},
	ActionSpend:         {FieldSessionCost, FieldBudgetUsed, FieldWorkflow, FieldAgent},
	ActionModelRoute:    {FieldPlanLeft, FieldModelTier, FieldWorkflow, FieldAgent},
	ActionShare:         {FieldDestination},
}

// Qualifiers narrow a condition; they are not conditions of their own.
const (
	// windowKey qualifies plan_left_pct.
	windowKey = "window"
	// budgetKey qualifies budget_used_pct with a budget's name.
	budgetKey = "budget"
)

// Destinations figures can leave for.
const (
	DestinationTeam = "team"
	DestinationOTel = "otel"
)

// Op is a comparator.
type Op string

// The comparators.
const (
	OpLT Op = "<"
	OpLE Op = "<="
	OpGT Op = ">"
	OpGE Op = ">="
	OpEQ Op = "=="
)

// Window is the plan window plan_left_pct reads.
type Window string

// The windows. WindowAny takes the tightest window known.
const (
	WindowAny  Window = ""
	Window5h   Window = "5h"
	WindowWeek Window = "week"
)

// RequireKind is what a matching rule requires.
type RequireKind string

// The requirements, loosest first.
const (
	RequireAllow    RequireKind = "allow"
	RequireAsk      RequireKind = "ask"
	RequireApprover RequireKind = "approver"
	RequireDeny     RequireKind = "deny"
)

// Require is a rule's requirement. Role is set for RequireApprover.
type Require struct {
	Kind RequireKind
	Role string
}

// String renders the requirement as it is written in config.
func (r Require) String() string {
	if r.Kind == RequireApprover {
		return "approver:" + r.Role
	}
	return string(r.Kind)
}

// Authority places the requirement on the ladder: allow is Automatic,
// ask and approver are RequireApproval, and deny is ObserveOnly — the
// rung at which nothing may change, which for an agent's action is a
// refusal.
func (r Require) Authority() policy.Authority {
	switch r.Kind {
	case RequireAllow:
		return policy.Automatic
	case RequireAsk, RequireApprover:
		return policy.RequireApproval
	default:
		return policy.ObserveOnly
	}
}

// strictness orders requirements. It follows the ladder and breaks the
// RequireApproval tie: a named approver is stricter than anyone at the
// keyboard.
func (r Require) strictness() int {
	switch r.Kind {
	case RequireAllow:
		return 0
	case RequireAsk:
		return 1
	case RequireApprover:
		return 2
	default:
		return 3
	}
}

// LocalAsk reports whether the requirement is answered by someone: ask,
// or a named approver. Whether a named approver is answered here or by the
// organisation is the rule's to say (Rule.TeamRouted).
func (r Require) LocalAsk() bool { return r.Kind == RequireAsk || r.Kind == RequireApprover }

// Stage is whether a rule binds.
type Stage string

// The stages.
const (
	// StageShadow records what the rule would have done and binds nothing.
	StageShadow Stage = "shadow"
	// StageEnforce binds.
	StageEnforce Stage = "enforce"
)

// Condition is one figure compared against a value.
type Condition struct {
	Field  Field
	Op     Op
	Number float64
	Text   string
	// Window qualifies FieldPlanLeft.
	Window Window
	// Budget qualifies FieldBudgetUsed; empty takes the fullest budget.
	Budget string
}

// Source is where a rule was set.
type Source string

// The sources.
const (
	// SourceLocal is this machine's config.yaml.
	SourceLocal Source = ""
	// SourceOrg is the organisation's rules, delivered by the team plane
	// (ADR 0014 §5).
	SourceOrg Source = "org"
)

// OrgPrefix marks an organisation rule's name wherever it is shown or
// keyed, so it never collides with a local rule of the same name.
const OrgPrefix = "org:"

// Rule is one boundary.
type Rule struct {
	Name    string
	Action  Action
	When    []Condition
	Require Require
	Stage   Stage
	Source  Source
}

// Enforced reports whether the rule binds.
func (r Rule) Enforced() bool { return r.Stage == StageEnforce }

// TeamRouted reports whether the rule's approval is routed to a role in
// the organisation rather than answered on this machine: an organisation
// rule that names an approver. A member cannot approve past their
// organisation's rule themselves.
func (r Rule) TeamRouted() bool { return r.Source == SourceOrg && r.Require.Kind == RequireApprover }

// FromOrg marks rules as the organisation's: their names gain OrgPrefix
// and their source is SourceOrg. The input is not changed.
func FromOrg(rules []Rule) []Rule {
	out := slices.Clone(rules)
	for i := range out {
		out[i].Name = OrgPrefix + out[i].Name
		out[i].Source = SourceOrg
	}
	return out
}

// RuleSpec is a rule as written in config.yaml.
type RuleSpec struct {
	Name    string            `yaml:"name" json:"name"`
	Action  string            `yaml:"action" json:"action"`
	When    map[string]string `yaml:"when,omitempty" json:"when,omitempty"`
	Require string            `yaml:"require" json:"require"`
	// Stage is shadow (the default) or enforce. `tokenops boundaries
	// promote` sets it.
	Stage string `yaml:"stage,omitempty" json:"stage,omitempty"`
}

var namePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

// ValidName reports whether s is a rule or role name: lower-case letters,
// digits, - and _, at most 64.
func ValidName(s string) bool { return namePattern.MatchString(s) }

// Parse validates rules as written in config. Every error names the rule
// it is about.
func Parse(specs []RuleSpec) ([]Rule, error) {
	out := make([]Rule, 0, len(specs))
	seen := map[string]bool{}
	for i, s := range specs {
		r, err := parseRule(s)
		if err != nil {
			if s.Name == "" {
				return nil, fmt.Errorf("boundaries[%d]: %w", i, err)
			}
			return nil, fmt.Errorf("boundaries[%d] %q: %w", i, s.Name, err)
		}
		if seen[r.Name] {
			return nil, fmt.Errorf("boundaries[%d]: rule %q is defined twice", i, r.Name)
		}
		seen[r.Name] = true
		out = append(out, r)
	}
	return out, nil
}

func parseRule(s RuleSpec) (Rule, error) {
	name := strings.TrimSpace(s.Name)
	if name == "" {
		return Rule{}, fmt.Errorf("name is required")
	}
	if !namePattern.MatchString(name) {
		return Rule{}, fmt.Errorf("name %q: use lower-case letters, digits, - and _ (at most 64)", name)
	}
	action := Action(strings.ToLower(strings.TrimSpace(s.Action)))
	if !slices.Contains(actions, action) {
		return Rule{}, fmt.Errorf("action %q is not one of %s", s.Action, joinActions())
	}
	req, err := ParseRequire(s.Require)
	if err != nil {
		return Rule{}, err
	}
	stage, err := parseStage(s.Stage)
	if err != nil {
		return Rule{}, err
	}
	when, err := parseWhen(action, s.When)
	if err != nil {
		return Rule{}, err
	}
	return Rule{Name: name, Action: action, When: when, Require: req, Stage: stage}, nil
}

func joinActions() string {
	names := make([]string, len(actions))
	for i, a := range actions {
		names[i] = string(a)
	}
	return strings.Join(names, ", ")
}

// ParseRequire reads allow | ask | approver:<role> | deny.
func ParseRequire(s string) (Require, error) {
	v := strings.ToLower(strings.TrimSpace(s))
	switch v {
	case string(RequireAllow), string(RequireAsk), string(RequireDeny):
		return Require{Kind: RequireKind(v)}, nil
	}
	if role, ok := strings.CutPrefix(v, "approver:"); ok {
		role = strings.TrimSpace(role)
		if role == "" {
			return Require{}, fmt.Errorf("require %q names no role", s)
		}
		if !namePattern.MatchString(role) {
			return Require{}, fmt.Errorf("require %q: role must be lower-case letters, digits, - and _", s)
		}
		return Require{Kind: RequireApprover, Role: role}, nil
	}
	return Require{}, fmt.Errorf("require %q is not one of allow, ask, approver:<role>, deny", s)
}

func parseStage(s string) (Stage, error) {
	switch v := strings.ToLower(strings.TrimSpace(s)); v {
	case "", string(StageShadow):
		return StageShadow, nil
	case string(StageEnforce):
		return StageEnforce, nil
	default:
		return "", fmt.Errorf("stage %q is not shadow or enforce", s)
	}
}

func parseWhen(action Action, when map[string]string) ([]Condition, error) {
	for k := range when {
		if k == windowKey || k == budgetKey {
			continue
		}
		if !slices.Contains(fieldOrder, Field(k)) {
			return nil, fmt.Errorf("when.%s is not a condition (use %s)", k, joinFields())
		}
		if !slices.Contains(fieldsFor[action], Field(k)) {
			return nil, fmt.Errorf("when.%s does not apply to %s rules (they take %s)", k, action, joinFieldList(fieldsFor[action]))
		}
	}
	window, hasWindow := when[windowKey]
	if _, ok := when[string(FieldPlanLeft)]; hasWindow && !ok {
		return nil, fmt.Errorf("when.window qualifies plan_left_pct, which the rule does not set")
	}
	budget, hasBudget := when[budgetKey]
	if _, ok := when[string(FieldBudgetUsed)]; hasBudget && !ok {
		return nil, fmt.Errorf("when.budget qualifies budget_used_pct, which the rule does not set")
	}
	var out []Condition
	for _, f := range fieldOrder {
		raw, ok := when[string(f)]
		if !ok {
			continue
		}
		c, err := parseCondition(f, raw)
		if err != nil {
			return nil, fmt.Errorf("when.%s: %w", f, err)
		}
		switch f {
		case FieldPlanLeft:
			w, err := parseWindow(window)
			if err != nil {
				return nil, fmt.Errorf("when.window: %w", err)
			}
			c.Window = w
		case FieldBudgetUsed:
			c.Budget = strings.TrimSpace(budget)
			if hasBudget && c.Budget == "" {
				return nil, fmt.Errorf("when.budget names no budget")
			}
		}
		out = append(out, c)
	}
	return out, nil
}

func joinFields() string { return joinFieldList(fieldOrder) }

func joinFieldList(fields []Field) string {
	names := make([]string, len(fields))
	for i, f := range fields {
		names[i] = string(f)
	}
	return strings.Join(names, ", ")
}

func parseWindow(s string) (Window, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "any":
		return WindowAny, nil
	case "5h", "five_hour", "session":
		return Window5h, nil
	case "week", "weekly", "seven_day", "7d":
		return WindowWeek, nil
	default:
		return "", fmt.Errorf("window %q is not 5h or week", s)
	}
}

// splitOp cuts the comparator off a condition's value.
func splitOp(raw string) (Op, string, bool) {
	v := strings.TrimSpace(raw)
	for _, op := range []Op{OpLE, OpGE, OpEQ, OpLT, OpGT} {
		if rest, ok := strings.CutPrefix(v, string(op)); ok {
			return op, strings.TrimSpace(rest), true
		}
	}
	return "", v, false
}

func parseCondition(f Field, raw string) (Condition, error) {
	op, value, ok := splitOp(raw)
	if !ok {
		return Condition{}, fmt.Errorf("%q needs a comparator (<, <=, >, >=, ==), e.g. \">= 20\"", raw)
	}
	c := Condition{Field: f, Op: op}
	switch f {
	case FieldSessionCost, FieldPlanLeft, FieldBudgetUsed:
		n, err := strconv.ParseFloat(value, 64)
		if err != nil || math.IsNaN(n) || math.IsInf(n, 0) {
			return Condition{}, fmt.Errorf("%q is not a number", value)
		}
		if n < 0 {
			return Condition{}, fmt.Errorf("%v must not be negative", n)
		}
		if f != FieldSessionCost && n > 100 {
			return Condition{}, fmt.Errorf("%v must be between 0 and 100", n)
		}
		c.Number = n
	case FieldModelTier:
		tier := TierOf(value)
		if tier == "" {
			return Condition{}, fmt.Errorf("tier %q is not haiku, sonnet or opus", value)
		}
		c.Text = tier
	case FieldDestination:
		if op != OpEQ {
			return Condition{}, fmt.Errorf("destination only takes ==")
		}
		d := strings.ToLower(value)
		if d != DestinationTeam && d != DestinationOTel {
			return Condition{}, fmt.Errorf("destination %q is not team or otel", value)
		}
		c.Text = d
	case FieldWorkflow, FieldAgent:
		if op != OpEQ {
			return Condition{}, fmt.Errorf("%s only takes ==", f)
		}
		if value == "" {
			return Condition{}, fmt.Errorf("%q names nothing", raw)
		}
		if _, err := path.Match(value, ""); err != nil {
			return Condition{}, fmt.Errorf("%q is not a valid pattern", value)
		}
		c.Text = value
	}
	return c, nil
}

// tierRank orders the tiers a model_tier condition compares.
var tierRank = map[string]int{"haiku": 1, "sonnet": 2, "opus": 3}

// TierOf places a model or tier name on haiku < sonnet < opus. The
// catalog's tier names map on too: lookup is haiku; balanced and default
// are sonnet; deep is opus. Anything else is "".
func TierOf(name string) string {
	n := strings.ToLower(strings.TrimSpace(name))
	switch {
	case strings.Contains(n, "opus"), n == "deep":
		return "opus"
	case strings.Contains(n, "sonnet"), n == "balanced", n == "default":
		return "sonnet"
	case strings.Contains(n, "haiku"), n == "lookup":
		return "haiku"
	default:
		return ""
	}
}

// Figure is a number that may not be known. An unknown figure never
// satisfies a condition: a boundary that cannot read its figure lets the
// action through rather than guess.
type Figure struct {
	Value float64
	Known bool
}

// Known is a known figure.
func Known(v float64) Figure { return Figure{Value: v, Known: true} }

// Facts are the figures one evaluation reads.
type Facts struct {
	SessionCostUSD Figure
	// PlanLeftPct is the share of each plan window left, by window. A
	// window absent from the map is unknown.
	PlanLeftPct map[Window]float64
	// PlanVendor names whose plan PlanLeftPct reads ("Claude"); empty
	// when it is the tightest across every plan.
	PlanVendor string
	// BudgetUsedPct is the share of each configured budget used in its
	// current window, by budget name. A budget absent is unknown.
	BudgetUsedPct map[string]float64
	// ModelTier is haiku, sonnet or opus; empty is unknown. For
	// model_route it is the tier the router would move the request to.
	ModelTier string
	// Destination is where shared figures go: team or otel.
	Destination string
	WorkflowID  string
	AgentID     string
}

// Crossing is one figure that crossed a condition, as numbers and
// enumerations only: it is what an approval request carries off the
// machine (ADR 0014 §4), so it holds no name a member typed.
type Crossing struct {
	Field Field
	Op    Op
	// Value is the figure; Threshold, what the condition compares it to.
	// A model tier is its rank: haiku 1, sonnet 2, opus 3.
	Value     float64
	Threshold float64
	// Window qualifies plan_left_pct; Destination is set for destination.
	Window      Window
	Destination string
}

// Match is a rule that matched, with the figures that crossed it.
type Match struct {
	Rule      Rule
	crossed   []string
	crossings []Crossing
}

// Crossings are the figures that crossed the rule. A budget's name is
// left out: it is the member's own label.
func (m Match) Crossings() []Crossing { return m.crossings }

// Crossed says what was crossed, in a sentence fragment a person and an
// agent can act on.
func (m Match) Crossed() string {
	if len(m.crossed) == 0 {
		return "rule " + m.Rule.Name + " applies to every " + string(m.Rule.Action)
	}
	return strings.Join(m.crossed, ", ")
}

// RecordedMatch rebuilds a match from what a decision recorded, for the
// record of an answer given later.
func RecordedMatch(r Rule, crossed string) Match {
	m := Match{Rule: r}
	if crossed != "" {
		m.crossed = []string{crossed}
	}
	return m
}

// Match reports whether the rule applies to action given facts.
func (r Rule) Match(action Action, f Facts) (Match, bool) {
	if r.Action != action {
		return Match{}, false
	}
	m := Match{Rule: r}
	for _, c := range r.When {
		text, ok := c.holds(f)
		if !ok {
			return Match{}, false
		}
		if text != "" {
			m.crossed = append(m.crossed, text)
		}
		if x, ok := c.crossing(f); ok {
			m.crossings = append(m.crossings, x)
		}
	}
	return m, true
}

func (c Condition) holds(f Facts) (string, bool) {
	switch c.Field {
	case FieldSessionCost:
		if !f.SessionCostUSD.Known || !compare(f.SessionCostUSD.Value, c.Op, c.Number) {
			return "", false
		}
		return fmt.Sprintf("session cost $%.2f %s $%.2f", f.SessionCostUSD.Value, c.Op, c.Number), true
	case FieldPlanLeft:
		left, window, ok := planLeft(f.PlanLeftPct, c.Window)
		if !ok || !compare(left, c.Op, c.Number) {
			return "", false
		}
		return strings.TrimSpace(fmt.Sprintf("%s plan left %.1f%% %s %g%% (%s)", f.PlanVendor, left, c.Op, c.Number, window)), true
	case FieldBudgetUsed:
		used, name, ok := budgetUsed(f.BudgetUsedPct, c.Budget)
		if !ok || !compare(used, c.Op, c.Number) {
			return "", false
		}
		return budgetLeftWords(name, used, c), true
	case FieldModelTier:
		got, ok := tierRank[f.ModelTier]
		if !ok || !compare(float64(got), c.Op, float64(tierRank[c.Text])) {
			return "", false
		}
		return fmt.Sprintf("model %s %s %s", f.ModelTier, c.Op, c.Text), true
	case FieldDestination:
		if f.Destination != c.Text {
			return "", false
		}
		return "figures leaving for " + destinationName(f.Destination), true
	case FieldWorkflow:
		ok, _ := path.Match(c.Text, f.WorkflowID)
		return "", ok && f.WorkflowID != ""
	case FieldAgent:
		ok, _ := path.Match(c.Text, f.AgentID)
		return "", ok && f.AgentID != ""
	}
	return "", false
}

// crossing is the figure that held c, for conditions on figures; ids and
// patterns cross nothing.
func (c Condition) crossing(f Facts) (Crossing, bool) {
	x := Crossing{Field: c.Field, Op: c.Op, Threshold: c.Number}
	switch c.Field {
	case FieldSessionCost:
		x.Value = f.SessionCostUSD.Value
	case FieldPlanLeft:
		left, window, _ := planLeft(f.PlanLeftPct, c.Window)
		x.Value, x.Window = left, window
	case FieldBudgetUsed:
		x.Value, _, _ = budgetUsed(f.BudgetUsedPct, c.Budget)
	case FieldModelTier:
		x.Value, x.Threshold = float64(tierRank[f.ModelTier]), float64(tierRank[c.Text])
	case FieldDestination:
		x.Destination = f.Destination
	default:
		return Crossing{}, false
	}
	return x, true
}

// planLeft reads the named window, or the tightest one known.
func planLeft(left map[Window]float64, w Window) (float64, Window, bool) {
	if w != WindowAny {
		v, ok := left[w]
		return v, w, ok
	}
	best, bestW, found := 0.0, WindowAny, false
	for _, cand := range []Window{Window5h, WindowWeek} {
		if v, ok := left[cand]; ok && (!found || v < best) {
			best, bestW, found = v, cand, true
		}
	}
	return best, bestW, found
}

// budgetUsed reads the named budget, or the fullest one known. Ties go
// to the name that sorts first, so the reason reads the same every run.
// budgetLeftWords words a budget condition that holds by what is left,
// the way every plan window and budget reads, and keeps the rule's own
// comparison on the share spent readable beside it: "budget monthly 8.0%
// left (92.0% spent >= 90%)".
func budgetLeftWords(name string, used float64, c Condition) string {
	left := fmt.Sprintf("%.1f%% left", max(0, 100-used))
	if used >= 100 {
		left = "used up"
	}
	return fmt.Sprintf("budget %s %s (%.1f%% spent %s %g%%)", name, left, used, c.Op, c.Number)
}

func budgetUsed(used map[string]float64, name string) (float64, string, bool) {
	if name != "" {
		v, ok := used[name]
		return v, name, ok
	}
	best, bestName, found := 0.0, "", false
	for n, v := range used {
		if !found || v > best || (v == best && n < bestName) {
			best, bestName, found = v, n, true
		}
	}
	return best, bestName, found
}

func destinationName(d string) string {
	if d == DestinationOTel {
		return "the OTel collector"
	}
	return "the team plane"
}

func compare(a float64, op Op, b float64) bool {
	switch op {
	case OpLT:
		return a < b
	case OpLE:
		return a <= b
	case OpGT:
		return a > b
	case OpGE:
		return a >= b
	case OpEQ:
		return a == b
	}
	return false
}

// Verdict is every rule that matched one action, and the one that binds.
type Verdict struct {
	// Matches are every matching rule, shadow and enforced, in config
	// order.
	Matches []Match
	// Binding is the strictest enforced match; nil when no enforced rule
	// matched.
	Binding *Match
}

// Shadow are the matches that bind nothing.
func (v Verdict) Shadow() []Match {
	var out []Match
	for _, m := range v.Matches {
		if !m.Rule.Enforced() {
			out = append(out, m)
		}
	}
	return out
}

// Refuses reports whether the binding rule denies the action.
func (v Verdict) Refuses() bool {
	return v.Binding != nil && v.Binding.Rule.Require.Kind == RequireDeny
}

// Asks reports whether the binding rule needs someone's approval.
func (v Verdict) Asks() bool { return v.Binding != nil && v.Binding.Rule.Require.LocalAsk() }

// Evaluate matches every rule for action against facts. The strictest
// enforced match binds, as policy.Effective takes the lesser rung.
func Evaluate(rules []Rule, action Action, f Facts) Verdict {
	var v Verdict
	for _, r := range rules {
		m, ok := r.Match(action, f)
		if !ok {
			continue
		}
		v.Matches = append(v.Matches, m)
		if !r.Enforced() {
			continue
		}
		if v.Binding == nil || r.Require.strictness() > v.Binding.Rule.Require.strictness() {
			bound := m
			v.Binding = &bound
		}
	}
	return v
}

// Promote returns rules with the named one enforced. The input is not
// changed.
func Promote(rules []Rule, name string) []Rule {
	out := slices.Clone(rules)
	for i := range out {
		if out[i].Name == name {
			out[i].Stage = StageEnforce
		}
	}
	return out
}

// DecisionID names the decision a rule makes in one session. It is stable,
// so an agent that retries a refused call is refused under the same id
// the operator was told to approve.
func DecisionID(rule, session string) string {
	sum := sha256.Sum256([]byte(rule + "\x00" + session))
	return "bnd-" + hex.EncodeToString(sum[:6])
}

var decisionIDPattern = regexp.MustCompile(`^bnd-[0-9a-f]{12}$`)

// ValidDecisionID reports whether id has DecisionID's shape. Callers use
// it before an id names a file.
func ValidDecisionID(id string) bool { return decisionIDPattern.MatchString(id) }
