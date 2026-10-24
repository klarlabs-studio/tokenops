// Package boundaries is the boundary engine's use cases (ADR 0014): the
// pause point every hook calls before a tool call or a subagent, the
// operator's answers, and the status of each rule. The CLI, the MCP server
// and the daemon API all call it.
package boundaries

import (
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"

	"go.klarlabs.de/tokenops/internal/capability/explain"
	domain "go.klarlabs.de/tokenops/internal/contexts/boundaries"
	"go.klarlabs.de/tokenops/internal/contexts/boundaries/presence"
	"go.klarlabs.de/tokenops/internal/infra/boundaryledger"
	"go.klarlabs.de/tokenops/internal/infra/claudelimits"
	"go.klarlabs.de/tokenops/internal/infra/coachhook"
	"go.klarlabs.de/tokenops/internal/infra/routeguard"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// Rule is a boundary rule, for adapters that pass rules through without
// reaching into the domain.
type Rule = domain.Rule

// Harnesses that can refuse a call, and the ones that cannot.
const (
	HarnessClaudeCode = "claude-code"
	HarnessOpencode   = "opencode"
	HarnessCodex      = "codex"
	HarnessCursor     = "cursor"
)

// Decision points inside TokenOps: the router, which decides model_route,
// and TokenOps itself, which decides share before figures leave.
const (
	PointRouter   = "router"
	PointTokenOps = "tokenops"
)

// limitsFreshFor is how old the status line's plan reading may be and
// still count. Claude Code refreshes it every turn; an older one belongs
// to a session that stopped, and a stale figure must not refuse anything.
const limitsFreshFor = 30 * time.Minute

// figuresFreshFor is how old the daemon's figures may be. It writes them
// every minute; older ones mean it stopped, and its readings with it.
const figuresFreshFor = 5 * time.Minute

// Env is where the pause point reads and writes. The zero value uses the
// defaults under ~/.tokenops.
type Env struct {
	Dir boundaryledger.Dir
	// CoachDir is the coach hook's state, which holds each session's
	// running cost.
	CoachDir string
	// LimitsPath is the plan reading the status line keeps.
	LimitsPath string
	Now        func() time.Time
	// User is the local account answers and actors are recorded under.
	User string
	// Attended is whether someone can answer a permission prompt.
	Attended bool
	// PresenceDir is the presence key store (ADR 0015); empty is
	// presence/ beside Dir.
	PresenceDir string
	// Approval is how approvals are checked (boundary_approval); zero is
	// channel, where the presence check is in shadow.
	Approval presence.Mode
}

func (e Env) approval() presence.Mode {
	if e.Approval == "" {
		return presence.ModeChannel
	}
	return e.Approval
}

func (e Env) now() time.Time {
	if e.Now != nil {
		return e.Now().UTC()
	}
	return time.Now().UTC()
}

func (e Env) user() string {
	if e.User != "" {
		return e.User
	}
	return LocalUser()
}

// LocalUser is the account this process runs as.
func LocalUser() string {
	if u, err := user.Current(); err == nil && u.Username != "" {
		return u.Username
	}
	if v := os.Getenv("USER"); v != "" {
		return v
	}
	return "local"
}

// Call is one action the pause point is asked about. It carries ids and
// a model name, never a prompt or a tool's input.
type Call struct {
	Harness        string
	SessionID      string
	TranscriptPath string
	ToolUseID      string
	// AgentID and WorkflowID are the attribution budgets use
	// ("claude-code:<project>").
	AgentID    string
	WorkflowID string
	Action     domain.Action
	// Model is the subagent's model, for subagent_model, or the model the
	// router would move a request to, for model_route.
	Model string
	// Provider is the provider whose plan headroom applies; empty takes
	// the tightest plan known. Claude Code's is always anthropic.
	Provider string
	// Destination is where shared figures go, for share.
	Destination string
}

// Attribution derives the agent and workflow ids every other surface
// stamps on a session ("claude-code:<project>"), from the project: the
// directory a Claude Code transcript sits in, or the one opencode names.
func Attribution(harness, sessionID, project string) (agentID, workflowID string) {
	if project == "" || project == "." || project == string(filepath.Separator) {
		return harness, harness + ":" + sessionID
	}
	return harness + ":" + project, harness + ":" + project + ":" + sessionID
}

// TranscriptProject is the project a Claude Code transcript belongs to:
// the directory it sits in under ~/.claude/projects.
func TranscriptProject(transcriptPath string) string {
	if transcriptPath == "" {
		return ""
	}
	return filepath.Base(filepath.Dir(transcriptPath))
}

// Verdicts the pause point returns.
const (
	// VerdictNone lets the call through with nothing said.
	VerdictNone = ""
	// VerdictAsk asks the person at the keyboard.
	VerdictAsk = "ask"
	// VerdictDeny refuses, with a reason the agent can act on.
	VerdictDeny = "deny"
)

// Result is what the pause point decided.
type Result struct {
	Verdict    string
	Reason     string
	DecisionID string
}

// Pause is what the pause point checks before every tool call: the
// continue and spend rules.
func Pause(rules []domain.Rule, call Call, env Env) (Result, error) {
	return GuardActions(rules, call, env, domain.ActionContinue, domain.ActionSpend)
}

// GuardActions evaluates call against each action in turn — the pause
// point checks continue and spend before the same tool call — and returns
// the strictest result: a refusal over a question over nothing.
func GuardActions(rules []domain.Rule, call Call, env Env, actions ...domain.Action) (Result, error) {
	var out Result
	for _, a := range actions {
		c := call
		c.Action = a
		res, err := Guard(rules, c, env)
		if err != nil {
			return Result{}, err
		}
		if verdictRank(res.Verdict) > verdictRank(out.Verdict) {
			out = res
		}
	}
	return out, nil
}

func verdictRank(v string) int {
	switch v {
	case VerdictDeny:
		return 2
	case VerdictAsk:
		return 1
	default:
		return 0
	}
}

// Guard evaluates the rules for call. Shadow matches are recorded and
// change nothing; the strictest enforced match asks or refuses unless the
// operator already answered it.
//
// It reads two small files at most — the session's cost and the plan
// reading — and only when a rule for this action needs them. A call with
// no rule for its action reads nothing.
func Guard(rules []domain.Rule, call Call, env Env) (Result, error) {
	relevant := forAction(rules, call.Action)
	if len(relevant) == 0 {
		return Result{}, nil
	}
	facts, evidence := readFacts(relevant, call, env)
	v := domain.Evaluate(relevant, call.Action, facts)
	if len(v.Matches) == 0 {
		return Result{}, nil
	}
	g := &guard{call: call, env: env, evidence: evidence, now: env.now()}
	g.load()
	matched := make([]string, 0, len(v.Matches))
	for _, m := range v.Matches {
		matched = append(matched, m.Rule.Name)
		if !m.Rule.Enforced() {
			if err := g.recordOnce(m, eventschema.DecisionStageShadow, eventschema.DecisionApproval{}); err != nil {
				return Result{}, err
			}
		}
	}
	res, err := g.bind(v)
	if err != nil {
		return Result{}, err
	}
	if err := env.Dir.AddMatches(call.SessionID, matched, g.now); err != nil {
		return Result{}, err
	}
	return res, g.save()
}

func forAction(rules []domain.Rule, action domain.Action) []domain.Rule {
	var out []domain.Rule
	for _, r := range rules {
		if r.Action == action {
			out = append(out, r)
		}
	}
	return out
}

func needs(rules []domain.Rule, f domain.Field) bool {
	for _, r := range rules {
		for _, c := range r.When {
			if c.Field == f {
				return true
			}
		}
	}
	return false
}

// readFacts reads only the figures the rules compare.
func readFacts(rules []domain.Rule, call Call, env Env) (domain.Facts, []eventschema.EvidenceRef) {
	now := env.now()
	f := domain.Facts{
		AgentID: call.AgentID, WorkflowID: call.WorkflowID, ModelTier: domain.TierOf(call.Model),
		Destination: call.Destination,
	}
	var ev []eventschema.EvidenceRef
	figs := lazyFigures{dir: env.Dir, now: now}
	if needs(rules, domain.FieldSessionCost) {
		if usd, ok := coachhook.SessionCostUSD(env.CoachDir, call.SessionID); ok {
			f.SessionCostUSD = domain.Known(usd)
			ev = append(ev, eventschema.EvidenceRef{
				Kind: "session_cost", Source: "coach_hook_ledger", ObservedAt: now, Scope: call.SessionID,
				Caveat: "summed at the session's last turn end, API-equivalent",
			})
		}
	}
	if needs(rules, domain.FieldPlanLeft) {
		provider := call.Provider
		if call.Harness == HarnessClaudeCode {
			provider = "anthropic"
			if left, observed, ok := planLeft(env.LimitsPath, now); ok {
				f.PlanLeftPct = left
				ev = append(ev, eventschema.EvidenceRef{
					Kind: "plan_headroom", Source: "claude_code_statusline", ObservedAt: observed,
					FreshUntil: observed.Add(limitsFreshFor), Confidence: 1, Scope: "anthropic",
				})
			}
		}
		if f.PlanLeftPct == nil {
			if fig, ok := figs.get(); ok {
				if left := figuresPlanLeft(fig, provider); len(left) > 0 {
					f.PlanLeftPct = left
					scope, caveat := provider, ""
					if scope == "" {
						scope, caveat = "any", "the tightest plan TokenOps watches, whichever provider"
					}
					ev = append(ev, eventschema.EvidenceRef{
						Kind: "plan_headroom", Source: "daemon_glance", ObservedAt: fig.ObservedAt,
						FreshUntil: fig.ObservedAt.Add(figuresFreshFor), Confidence: 1, Scope: scope, Caveat: caveat,
					})
				}
			}
		}
	}
	if needs(rules, domain.FieldBudgetUsed) {
		if fig, ok := figs.get(); ok && len(fig.BudgetUsedPct) > 0 {
			f.BudgetUsedPct = fig.BudgetUsedPct
			ev = append(ev, eventschema.EvidenceRef{
				Kind: "budget_used", Source: "daemon_budgets", ObservedAt: fig.ObservedAt,
				FreshUntil: fig.ObservedAt.Add(figuresFreshFor), Confidence: 1, Scope: "budgets",
			})
		}
	}
	return f, ev
}

// lazyFigures reads the daemon's figures at most once per evaluation, and
// only when a rule needs them.
type lazyFigures struct {
	dir  boundaryledger.Dir
	now  time.Time
	read bool
	fig  boundaryledger.Figures
	ok   bool
}

// get returns the figures when the daemon wrote them recently enough to
// count; stale ones say nothing.
func (l *lazyFigures) get() (boundaryledger.Figures, bool) {
	if !l.read {
		l.read = true
		fig, ok, err := l.dir.GetFigures()
		l.fig, l.ok = fig, ok && err == nil && l.now.Sub(fig.ObservedAt) <= figuresFreshFor
	}
	return l.fig, l.ok
}

// figuresPlanLeft is the provider's share left by window, or, with no
// provider, the tightest share each window has across every plan.
func figuresPlanLeft(fig boundaryledger.Figures, provider string) map[domain.Window]float64 {
	out := map[domain.Window]float64{}
	add := func(windows map[string]float64) {
		for name, left := range windows {
			w := domain.Window(name)
			if w != domain.Window5h && w != domain.WindowWeek {
				continue
			}
			if cur, ok := out[w]; !ok || left < cur {
				out[w] = left
			}
		}
	}
	if provider != "" {
		add(fig.PlanLeftPct[provider])
		return out
	}
	for _, windows := range fig.PlanLeftPct {
		add(windows)
	}
	return out
}

// planLeft is the share of each Claude plan window left, from the
// reading the status line keeps. A window that has reset since is
// unknown, not full: the reading no longer says anything about it.
func planLeft(path string, now time.Time) (map[domain.Window]float64, time.Time, bool) {
	r, ok, err := claudelimits.Read(path)
	if err != nil || !ok || now.Sub(r.ObservedAt) > limitsFreshFor {
		return nil, time.Time{}, false
	}
	out := map[domain.Window]float64{}
	for w, win := range map[domain.Window]*claudelimits.Window{domain.Window5h: r.FiveHour, domain.WindowWeek: r.SevenDay} {
		if win == nil || (win.ResetsAt > 0 && time.Unix(win.ResetsAt, 0).Before(now)) {
			continue
		}
		out[w] = max(0, 100-win.UsedPct)
	}
	return out, r.ObservedAt.UTC(), len(out) > 0
}

type guard struct {
	call     Call
	env      Env
	evidence []eventschema.EvidenceRef
	now      time.Time
	session  boundaryledger.Session
	// changed is set when a decision stage was recorded: only then is the
	// session's file written again. The matches go to its log.
	changed bool
	// refused is an approval enforcement did not honour on this call, said
	// in the refusal the agent reads.
	refused *refusedApproval
}

// refusedApproval is an answer the presence check did not honour.
type refusedApproval struct {
	method string
	reason presence.Reason
}

func (g *guard) load() {
	s, err := g.env.Dir.GetSession(g.call.SessionID)
	if err != nil {
		// A corrupt counter file must not refuse anything or fail every
		// call after it: start the counters again.
		s = boundaryledger.Session{}
	}
	if s.Recorded == nil {
		s.Recorded = map[string]string{}
	}
	g.session = s
}

func (g *guard) save() error {
	if !g.changed {
		return nil
	}
	g.session.LastAt = g.now
	return g.env.Dir.PutSession(g.call.SessionID, g.session)
}

// bind applies the binding match, if any.
func (g *guard) bind(v domain.Verdict) (Result, error) {
	if v.Binding == nil || v.Binding.Rule.Require.Kind == domain.RequireAllow {
		return Result{}, nil
	}
	m := *v.Binding
	id := domain.DecisionID(m.Rule.Name, g.call.SessionID)
	if m.Rule.Require.Kind == domain.RequireDeny {
		if err := g.recordOnce(m, eventschema.DecisionStageApplied, eventschema.DecisionApproval{}); err != nil {
			return Result{}, err
		}
		return Result{Verdict: VerdictDeny, DecisionID: id, Reason: denyReason(m)}, nil
	}
	if res, done, err := g.answered(m, id); done || err != nil {
		return res, err
	}
	routed := m.Rule.TeamRouted()
	// Under presence-strict the harness's prompt does not approve, so it
	// is not asked: the refusal names `tokenops approve`, which signs.
	prompt := g.env.Attended && !routed && !g.env.approval().Strict()
	if prompt {
		if done, err := g.settlePrompt(m, id); done || err != nil {
			return Result{}, err
		}
	}
	pending := boundaryledger.Pending{
		ID: id, Rule: m.Rule.Name, Require: m.Rule.Require.String(), Crossed: m.Crossed(),
		Action: string(g.call.Action), Harness: g.call.Harness, SessionID: g.call.SessionID,
		AgentID: g.call.AgentID, At: g.now, Crossings: m.Crossings(),
	}
	if prev, ok, _ := g.env.Dir.GetPending(id); ok {
		// Keep when the request first went to the organisation, so it is
		// sent once, not on every call while it waits.
		pending.RoutedAt = prev.RoutedAt
		if routed {
			pending.At = prev.At
		}
		// The raising goes on while it waits: keep its challenge, so an
		// approval signed for it still names it.
		pending.Challenge, pending.Spent = prev.Challenge, prev.Spent
	}
	if pending.Challenge == "" {
		pending.Challenge = newChallenge()
	}
	if prompt {
		pending.ToolUseID = g.call.ToolUseID
	}
	if err := g.env.Dir.PutPending(pending); err != nil {
		return Result{}, err
	}
	if err := g.recordOnce(m, eventschema.DecisionStageProposed, eventschema.DecisionApproval{}); err != nil {
		return Result{}, err
	}
	if routed {
		// Whoever is at the keyboard is not necessarily the role the
		// organisation named, so the prompt does not answer it.
		return Result{Verdict: VerdictDeny, DecisionID: id, Reason: routedReason(m, id)}, nil
	}
	if prompt && agentFacing(m.Rule.Action) {
		return Result{Verdict: VerdictAsk, DecisionID: id, Reason: askReason(m, id) + g.refusedNote()}, nil
	}
	return Result{Verdict: VerdictDeny, DecisionID: id, Reason: g.unattendedReason(m, id)}, nil
}

// refusedNote says, after a refusal, that an approval was given but not
// honoured, so the operator who approved sees why it did not count.
func (g *guard) refusedNote() string {
	if g.refused == nil {
		return ""
	}
	return fmt.Sprintf(" An approval (%s) was given but not honoured: %s.", g.refused.method, reasonPhrase(g.refused.reason))
}

// unattendedReason is the refusal with the way out. Under presence
// enforcement the way out depends on the key: approve (with the dialog),
// enrol one first, or wait out a replaced key's hold.
func (g *guard) unattendedReason(m domain.Match, id string) string {
	mode := g.env.approval()
	if !mode.Enforced() {
		return unattendedReason(m, id) + g.refusedNote()
	}
	head := fmt.Sprintf("tokenops boundary %q: %s, and this %s.", m.Rule.Name, m.Crossed(), approverPhrase(m.Rule.Require))
	if agentFacing(m.Rule.Action) {
		head += " Do not retry: stop and tell the operator."
	} else {
		head += " " + heldUntil(m.Rule.Action) + " until then."
	}
	trust, err := g.env.presenceDir().Trust(g.now)
	var way string
	switch {
	case err != nil:
		way = fmt.Sprintf(" Approval needs this Mac's presence key, and its key store could not be read; demote the rule with `tokenops boundaries demote %s` to let it through.", m.Rule.Name)
	case trust.State == presence.TrustNotEnrolled:
		way = fmt.Sprintf(" Approval needs this Mac's presence key (boundary_approval: %s) and none is enrolled: "+
			"enrol one with `%s`, then approve with `tokenops approve %s`; or demote the rule with `tokenops boundaries demote %s`.",
			mode, EnrolCommand, id, m.Rule.Name)
	case trust.State == presence.TrustHeld:
		way = fmt.Sprintf(" Approvals are held until %s: the presence key was replaced without the old key's signature. "+
			"After that, `tokenops approve %s` asks for Touch ID, a paired Apple Watch or the password.",
			trust.Pin.HoldUntil.Local().Format("15:04"), id)
	default:
		way = fmt.Sprintf(" They can allow this one step with `tokenops approve %s`, or the rest of the session with "+
			"`tokenops approve %s --session`; either asks for Touch ID, a paired Apple Watch or the password.", id, id)
	}
	return head + way + g.refusedNote()
}

// answered applies an answer the operator already gave.
func (g *guard) answered(m domain.Match, id string) (Result, bool, error) {
	a, ok, err := g.env.Dir.GetAnswer(id, g.now)
	if err != nil || !ok {
		return Result{}, false, err
	}
	approval := eventschema.DecisionApproval{Approver: a.Approver, Method: a.Method, At: a.At, Rule: a.Rule, Scope: a.Scope}
	if !a.Approved {
		if a.Scope == boundaryledger.ScopeOnce {
			if won, err := g.env.Dir.ConsumeAnswer(id); err != nil || !won {
				return Result{}, false, err
			}
		}
		return Result{Verdict: VerdictDeny, DecisionID: id, Reason: declinedReason(m, a.Approver)}, true, nil
	}
	// Under boundary_approval: channel the presence check is in shadow
	// (ADR 0015 §5): the approval is honoured as before, and the decision
	// records what the verifier would have said. Enforced, an approval the
	// check does not honour is spent unused and recorded, and the
	// decision goes on waiting.
	check := checkPresence(g.env, a, id, m.Rule.Name, g.call.SessionID, g.now)
	if mode := g.env.approval(); !check.honoured(mode) {
		return Result{}, false, g.refuse(m, id, a, check, mode)
	}
	approval = check.stamp(approval)
	if a.Scope == boundaryledger.ScopeOnce {
		won, err := g.env.Dir.ConsumeAnswer(id)
		if err != nil || !won {
			return Result{}, false, err
		}
		if err := retireChallenge(g.env, id, check.spent); err != nil {
			return Result{}, false, err
		}
		return Result{}, true, g.record(m, eventschema.DecisionStageApplied, approval)
	}
	return Result{}, true, g.recordOnce(m, eventschema.DecisionStageApplied, approval)
}

// refuse records an approval enforcement did not honour and removes it, so
// it is recorded once and the decision is raised again with the reason.
func (g *guard) refuse(m domain.Match, id string, a boundaryledger.Answer, check presenceCheck, mode presence.Mode) error {
	if _, err := g.env.Dir.ConsumeAnswer(id); err != nil {
		return err
	}
	reason := check.refusal(mode)
	g.refused = &refusedApproval{method: a.Method, reason: reason}
	approval := eventschema.DecisionApproval{
		Approver: a.Approver, Method: a.Method, At: a.At, Rule: a.Rule, Scope: a.Scope,
		Presence: PresenceRefused, PresenceKey: check.key, WouldRefuse: string(reason),
	}
	return g.record(m, eventschema.DecisionStageProposed, approval)
}

// settlePrompt reads back an ask raised at the permission prompt: a call
// that ran was approved, and the approval holds for the rest of the
// session so the operator is not asked again on every call.
//
// A call Claude Code stopped itself (its auto mode classifier, a
// permission rule, a plugin or a PreToolUse hook) never reached the
// prompt, because Claude Code settles the strictest answer first: the
// person was not asked, so it is no approval. Its verdict is kept as
// evidence on the decision and the ask moves to the next call, which is
// then the first time the person is asked, not the second.
func (g *guard) settlePrompt(m domain.Match, id string) (bool, error) {
	if !g.env.Attended || g.call.TranscriptPath == "" {
		return false, nil
	}
	p, ok, err := g.env.Dir.GetPending(id)
	if err != nil || !ok || p.ToolUseID == "" || p.ToolUseID == g.call.ToolUseID {
		return false, err
	}
	v, seen := routeguard.ToolVerdict(g.call.TranscriptPath, p.ToolUseID)
	switch {
	case !seen || v.Outcome == routeguard.OutcomeDeclined:
		return false, nil
	case v.Outcome == routeguard.OutcomeVendorStopped:
		return false, g.recordVendorStop(m, p.ToolUseID, v)
	}
	a := boundaryledger.Answer{
		ID: id, Rule: m.Rule.Name, SessionID: g.call.SessionID, Approved: true,
		Scope: boundaryledger.ScopeSession, Approver: g.env.user(), Method: "prompt", At: g.now,
	}
	if err := g.env.Dir.PutAnswer(a); err != nil {
		return false, err
	}
	approval := eventschema.DecisionApproval{
		Approver: a.Approver, Method: a.Method, At: a.At, Rule: a.Rule, Scope: a.Scope, Presence: PresenceHarness,
	}
	if err := g.record(m, eventschema.DecisionStageAccepted, approval); err != nil {
		return false, err
	}
	g.session.Recorded[id] = string(eventschema.DecisionStageAccepted)
	g.changed = true
	return true, nil
}

// recordVendorStop records, on the boundary's decision, that Claude Code
// stopped the call the ask was attached to: the vendor's verdict as
// evidence beside TokenOps' own, with the ask still open.
func (g *guard) recordVendorStop(m domain.Match, toolUseID string, v routeguard.Verdict) error {
	observed := v.At.UTC()
	if v.At.IsZero() {
		observed = g.now
	}
	evidence := append(append([]eventschema.EvidenceRef(nil), g.evidence...), eventschema.EvidenceRef{
		Kind: "vendor_verdict", Source: "claude_code_transcript", ObservedAt: observed, Confidence: 1,
		Scope:  g.call.SessionID + "/" + toolUseID,
		Caveat: "Claude Code stopped the call before any prompt (" + v.Stopper + "); read from the tool result's wording",
	})
	env := decisionEnvelope(m, eventschema.DecisionStageProposed, g.call, g.env, g.now, evidence, eventschema.DecisionApproval{})
	if d, ok := env.Payload.(*eventschema.DecisionEvent); ok {
		d.Rationale += "; Claude Code stopped the asked call itself (" + v.Stopper + "), so nobody answered and the ask moves to the next call"
	}
	return g.env.Dir.Append(env)
}

func approverPhrase(r domain.Require) string {
	if r.Kind == domain.RequireApprover {
		return fmt.Sprintf("needs approval from %s (answered on this machine until the team plane routes it)", r.Role)
	}
	return "needs the operator's approval"
}

// agentFacing reports whether an agent reads the refusal: the actions the
// pause point and route-guard decide. model_route and share are decided
// inside TokenOps, and their reasons are for the operator.
func agentFacing(a domain.Action) bool {
	return a == domain.ActionContinue || a == domain.ActionSpend || a == domain.ActionSubagentModel
}

func routedReason(m domain.Match, id string) string {
	if !agentFacing(m.Rule.Action) {
		return fmt.Sprintf("tokenops boundary %q (your organisation's): %s, and this needs approval from %s in your organisation. "+
			"The request has gone to them (%s). %s until they answer.",
			m.Rule.Name, m.Crossed(), m.Rule.Require.Role, id, heldUntil(m.Rule.Action))
	}
	return fmt.Sprintf("tokenops boundary %q (your organisation's): %s, and this needs approval from %s in your organisation. "+
		"The request has gone to them (%s). Do not retry: stop and tell the operator; the step is allowed once they approve it.",
		m.Rule.Name, m.Crossed(), m.Rule.Require.Role, id)
}

func askReason(m domain.Match, id string) string {
	return fmt.Sprintf("tokenops boundary %q: %s. Continue? Yes lets the agent go on for the rest of this session (%s).",
		m.Rule.Name, m.Crossed(), id)
}

func unattendedReason(m domain.Match, id string) string {
	if !agentFacing(m.Rule.Action) {
		return fmt.Sprintf("tokenops boundary %q: %s, and this %s. %s until then: "+
			"`tokenops approve %s` allows the next one, `tokenops approve %s --session` the next 24 hours.",
			m.Rule.Name, m.Crossed(), approverPhrase(m.Rule.Require), heldUntil(m.Rule.Action), id, id)
	}
	return fmt.Sprintf("tokenops boundary %q: %s, and this %s. Do not retry: stop and tell the operator. "+
		"They can allow this one step with `tokenops approve %s`, or the rest of the session with `tokenops approve %s --session`.",
		m.Rule.Name, m.Crossed(), approverPhrase(m.Rule.Require), id, id)
}

func denyReason(m domain.Match) string {
	if !agentFacing(m.Rule.Action) {
		return fmt.Sprintf("tokenops boundary %q: %s, and the rule refuses it. %s; only changing the rule in TokenOps' config.yaml lifts it.",
			m.Rule.Name, m.Crossed(), heldUntil(m.Rule.Action))
	}
	return fmt.Sprintf("tokenops boundary %q: %s, and the rule refuses any further %s. Do not retry: stop and tell the operator; "+
		"only changing the rule in TokenOps' config.yaml lifts it.", m.Rule.Name, m.Crossed(), actionNoun(m.Rule.Action))
}

func declinedReason(m domain.Match, approver string) string {
	if !agentFacing(m.Rule.Action) {
		return fmt.Sprintf("tokenops boundary %q: %s declined this (%s). %s.", m.Rule.Name, approver, m.Crossed(), heldUntil(m.Rule.Action))
	}
	return fmt.Sprintf("tokenops boundary %q: %s declined this (%s). Stop and ask the operator how to proceed.",
		m.Rule.Name, approver, m.Crossed())
}

// heldUntil says what a refused TokenOps-side action does instead.
func heldUntil(a domain.Action) string {
	if a == domain.ActionModelRoute {
		return "The request stays on the model it asked for"
	}
	return "The figures are not sent"
}

func actionNoun(a domain.Action) string {
	switch a {
	case domain.ActionSubagentModel:
		return "subagent on this model"
	case domain.ActionSpend:
		return "steps while it holds"
	default:
		return "steps in this session"
	}
}

// recordOnce records a stage for the rule's decision once per session.
func (g *guard) recordOnce(m domain.Match, stage eventschema.DecisionStage, approval eventschema.DecisionApproval) error {
	id := domain.DecisionID(m.Rule.Name, g.call.SessionID)
	if g.session.Recorded[id] == string(stage) {
		return nil
	}
	g.session.Recorded[id] = string(stage)
	g.changed = true
	return g.record(m, stage, approval)
}

func (g *guard) record(m domain.Match, stage eventschema.DecisionStage, approval eventschema.DecisionApproval) error {
	return g.env.Dir.Append(decisionEnvelope(m, stage, g.call, g.env, g.now, g.evidence, approval))
}

// decisionEnvelope is one boundary decision, in the record the router
// already writes (ADR 0014 §2).
func decisionEnvelope(m domain.Match, stage eventschema.DecisionStage, call Call, env Env, at time.Time,
	evidence []eventschema.EvidenceRef, approval eventschema.DecisionApproval) *eventschema.Envelope {
	id := domain.DecisionID(m.Rule.Name, call.SessionID)
	proceed := eventschema.ResourceOption{Model: call.Model, Reason: "proceed with the " + string(call.Action)}
	hold := eventschema.ResourceOption{Model: call.Model, Reason: "require " + m.Rule.Require.String()}
	selected := hold
	if stage == eventschema.DecisionStageShadow || stage == eventschema.DecisionStageAccepted ||
		(stage == eventschema.DecisionStageApplied && approval.Approver != "") {
		selected = proceed
	}
	rationale := m.Crossed()
	if stage == eventschema.DecisionStageShadow {
		rationale += "; in shadow, so it would have " + wouldHave(m.Rule.Require) + " and changed nothing"
	}
	rationale += presenceRationale(approval)
	return &eventschema.Envelope{
		ID: uuid.NewString(), SchemaVersion: eventschema.SchemaVersion, Type: eventschema.EventTypeDecision,
		Timestamp: at, Source: "boundary",
		Association: eventschema.Association{Actor: call.AgentID},
		Correlation: eventschema.Correlation{Decision: id},
		Payload: &eventschema.DecisionEvent{
			Kind: "boundary", Stage: stage,
			Alternatives: []eventschema.ResourceOption{proceed, hold}, Selected: selected,
			Evidence: evidence, Policy: explain.BoundaryPolicyPrefix + m.Rule.Name,
			Authority: m.Rule.Require.Authority().String(), Confidence: 1,
			Rationale: rationale, Executable: m.Rule.Enforced() && CanRefuse(call.Harness),
			Adapter: call.Harness + "-hook",
			Actor: eventschema.DecisionActor{
				Harness: call.Harness, SessionID: call.SessionID, AgentID: call.AgentID, User: env.user(),
			},
			Approval: approval,
		},
	}
}

func wouldHave(r domain.Require) string {
	switch r.Kind {
	case domain.RequireDeny:
		return "refused"
	case domain.RequireAllow:
		return "allowed"
	default:
		return "asked"
	}
}

// CanRefuse reports whether a harness, or a decision point inside
// TokenOps, can stop the action.
func CanRefuse(harness string) bool {
	switch harness {
	case HarnessClaudeCode, HarnessOpencode, PointRouter, PointTokenOps:
		return true
	}
	return false
}

// RecordFailure records that the pause point could not decide and let
// the call through, so a broken boundary is visible rather than silently
// absent (ADR 0014 §3). It is best effort: a ledger that cannot be
// written is the failure.
func RecordFailure(call Call, env Env, cause error) {
	if cause == nil {
		return
	}
	at := env.now()
	_ = env.Dir.Append(&eventschema.Envelope{
		ID: uuid.NewString(), SchemaVersion: eventschema.SchemaVersion, Type: eventschema.EventTypeDecision,
		Timestamp: at, Source: "boundary",
		Correlation: eventschema.Correlation{Decision: domain.DecisionID("_failed", call.SessionID)},
		Payload: &eventschema.DecisionEvent{
			Kind: "boundary", Stage: eventschema.DecisionStageFailed,
			Rationale: "the boundary check failed and the call went ahead (fail open): " + failureClass(cause),
			Adapter:   call.Harness + "-hook",
			Actor:     eventschema.DecisionActor{Harness: call.Harness, SessionID: call.SessionID, AgentID: call.AgentID, User: env.user()},
		},
	})
}

// failureClass keeps the cause's message to its first line, which is
// all an operator needs and keeps any long detail out of the record.
func failureClass(err error) string {
	msg, _, _ := strings.Cut(err.Error(), "\n")
	if len(msg) > 200 {
		msg = msg[:200]
	}
	return msg
}
