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
	"go.klarlabs.de/tokenops/internal/infra/boundaryledger"
	"go.klarlabs.de/tokenops/internal/infra/claudelimits"
	"go.klarlabs.de/tokenops/internal/infra/coachhook"
	"go.klarlabs.de/tokenops/internal/infra/routeguard"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// Harnesses that can refuse a call, and the ones that cannot.
const (
	HarnessClaudeCode = "claude-code"
	HarnessOpencode   = "opencode"
	HarnessCodex      = "codex"
	HarnessCursor     = "cursor"
)

// limitsFreshFor is how old the status line's plan reading may be and
// still count. Claude Code refreshes it every turn; an older one belongs
// to a session that stopped, and a stale figure must not refuse anything.
const limitsFreshFor = 30 * time.Minute

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
	// Model is the subagent's model, for subagent_model.
	Model string
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
	for _, m := range v.Matches {
		g.session.Matches[m.Rule.Name]++
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
	f := domain.Facts{AgentID: call.AgentID, WorkflowID: call.WorkflowID, ModelTier: domain.TierOf(call.Model)}
	var ev []eventschema.EvidenceRef
	if needs(rules, domain.FieldSessionCost) {
		if usd, ok := coachhook.SessionCostUSD(env.CoachDir, call.SessionID); ok {
			f.SessionCostUSD = domain.Known(usd)
			ev = append(ev, eventschema.EvidenceRef{
				Kind: "session_cost", Source: "coach_hook_ledger", ObservedAt: now, Scope: call.SessionID,
				Caveat: "summed at the session's last turn end, API-equivalent",
			})
		}
	}
	if needs(rules, domain.FieldPlanLeft) && call.Harness == HarnessClaudeCode {
		if left, observed, ok := planLeft(env.LimitsPath, now); ok {
			f.PlanLeftPct = left
			ev = append(ev, eventschema.EvidenceRef{
				Kind: "plan_headroom", Source: "claude_code_statusline", ObservedAt: observed,
				FreshUntil: observed.Add(limitsFreshFor), Confidence: 1, Scope: "anthropic",
			})
		}
	}
	return f, ev
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
}

func (g *guard) load() {
	s, err := g.env.Dir.GetSession(g.call.SessionID)
	if err != nil {
		// A corrupt counter file must not refuse anything or fail every
		// call after it: start the counters again.
		s = boundaryledger.Session{}
	}
	if s.Matches == nil {
		s.Matches = map[string]int{}
	}
	if s.Recorded == nil {
		s.Recorded = map[string]string{}
	}
	g.session = s
}

func (g *guard) save() error {
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
	if done, err := g.settlePrompt(m, id); done || err != nil {
		return Result{}, err
	}
	pending := boundaryledger.Pending{
		ID: id, Rule: m.Rule.Name, Require: m.Rule.Require.String(), Crossed: m.Crossed(),
		Action: string(g.call.Action), Harness: g.call.Harness, SessionID: g.call.SessionID,
		AgentID: g.call.AgentID, At: g.now,
	}
	if g.env.Attended {
		pending.ToolUseID = g.call.ToolUseID
	}
	if err := g.env.Dir.PutPending(pending); err != nil {
		return Result{}, err
	}
	if err := g.recordOnce(m, eventschema.DecisionStageProposed, eventschema.DecisionApproval{}); err != nil {
		return Result{}, err
	}
	if g.env.Attended {
		return Result{Verdict: VerdictAsk, DecisionID: id, Reason: askReason(m, id)}, nil
	}
	return Result{Verdict: VerdictDeny, DecisionID: id, Reason: unattendedReason(m, id)}, nil
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
		return Result{Verdict: VerdictDeny, DecisionID: id, Reason: fmt.Sprintf(
			"tokenops boundary %q: %s declined this (%s). Stop and ask the operator how to proceed.",
			m.Rule.Name, a.Approver, m.Crossed())}, true, nil
	}
	if a.Scope == boundaryledger.ScopeOnce {
		won, err := g.env.Dir.ConsumeAnswer(id)
		if err != nil || !won {
			return Result{}, false, err
		}
		return Result{}, true, g.record(m, eventschema.DecisionStageApplied, approval)
	}
	return Result{}, true, g.recordOnce(m, eventschema.DecisionStageApplied, approval)
}

// settlePrompt reads back an ask raised at the permission prompt: a call
// that ran was approved, and the approval holds for the rest of the
// session so the operator is not asked again on every call.
func (g *guard) settlePrompt(m domain.Match, id string) (bool, error) {
	if !g.env.Attended || g.call.TranscriptPath == "" {
		return false, nil
	}
	p, ok, err := g.env.Dir.GetPending(id)
	if err != nil || !ok || p.ToolUseID == "" || p.ToolUseID == g.call.ToolUseID {
		return false, err
	}
	approved, seen := routeguard.ToolAnswered(g.call.TranscriptPath, p.ToolUseID)
	if !seen || !approved {
		return false, nil
	}
	a := boundaryledger.Answer{
		ID: id, Rule: m.Rule.Name, SessionID: g.call.SessionID, Approved: true,
		Scope: boundaryledger.ScopeSession, Approver: g.env.user(), Method: "prompt", At: g.now,
	}
	if err := g.env.Dir.PutAnswer(a); err != nil {
		return false, err
	}
	approval := eventschema.DecisionApproval{Approver: a.Approver, Method: a.Method, At: a.At, Rule: a.Rule, Scope: a.Scope}
	if err := g.record(m, eventschema.DecisionStageAccepted, approval); err != nil {
		return false, err
	}
	g.session.Recorded[id] = string(eventschema.DecisionStageAccepted)
	return true, nil
}

func approverPhrase(r domain.Require) string {
	if r.Kind == domain.RequireApprover {
		return fmt.Sprintf("needs approval from %s (answered on this machine until the team plane routes it)", r.Role)
	}
	return "needs the operator's approval"
}

func askReason(m domain.Match, id string) string {
	return fmt.Sprintf("tokenops boundary %q: %s. Continue? Yes lets the agent go on for the rest of this session (%s).",
		m.Rule.Name, m.Crossed(), id)
}

func unattendedReason(m domain.Match, id string) string {
	return fmt.Sprintf("tokenops boundary %q: %s, and this %s. Do not retry: stop and tell the operator. "+
		"They can allow this one step with `tokenops approve %s`, or the rest of the session with `tokenops approve %s --session`.",
		m.Rule.Name, m.Crossed(), approverPhrase(m.Rule.Require), id, id)
}

func denyReason(m domain.Match) string {
	return fmt.Sprintf("tokenops boundary %q: %s, and the rule refuses any further %s. Do not retry: stop and tell the operator; "+
		"only changing the rule in TokenOps' config.yaml lifts it.", m.Rule.Name, m.Crossed(), actionNoun(m.Rule.Action))
}

func actionNoun(a domain.Action) string {
	if a == domain.ActionSubagentModel {
		return "subagent on this model"
	}
	return "steps in this session"
}

// recordOnce records a stage for the rule's decision once per session.
func (g *guard) recordOnce(m domain.Match, stage eventschema.DecisionStage, approval eventschema.DecisionApproval) error {
	id := domain.DecisionID(m.Rule.Name, g.call.SessionID)
	if g.session.Recorded[id] == string(stage) {
		return nil
	}
	g.session.Recorded[id] = string(stage)
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

// CanRefuse reports whether a harness has a hook that can stop a call.
func CanRefuse(harness string) bool {
	return harness == HarnessClaudeCode || harness == HarnessOpencode
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
