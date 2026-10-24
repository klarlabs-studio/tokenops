package boundaries

import (
	"fmt"
	"strings"

	"go.klarlabs.de/tokenops/internal/capability/actions"
	domain "go.klarlabs.de/tokenops/internal/contexts/boundaries"
	"go.klarlabs.de/tokenops/internal/contexts/boundaries/presence"
	"go.klarlabs.de/tokenops/internal/infra/boundaryledger"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// Answer methods: how an answer reached TokenOps.
const (
	MethodCLI    = "cli"
	MethodMCP    = "mcp"
	MethodAPI    = "api"
	MethodPrompt = "prompt"
)

// DecideRequest is the operator's answer to a boundary decision.
type DecideRequest struct {
	ID string `json:"id"`
	// Decision is approve or deny.
	Decision string `json:"decision"`
	// Session makes the answer hold for the rest of the session rather
	// than for the next step only.
	Session bool `json:"session,omitempty"`
	// Proof is the presence-key signature over this approval (ADR 0015):
	// SignApproval makes it. With it, an approval can come over the API;
	// with boundary_approval: presence, no approval counts without it.
	Proof  *presence.Proof `json:"presence_proof,omitempty"`
	Method string          `json:"-"`
}

// Decided is what an answer did.
type Decided struct {
	ID       string `json:"id"`
	Rule     string `json:"rule"`
	Decision string `json:"decision"`
	Scope    string `json:"scope"`
	Approver string `json:"approver"`
	// Presence is signed when the approval carried a valid presence proof,
	// with the key it was signed by.
	Presence    string `json:"presence,omitempty"`
	PresenceKey string `json:"presence_key,omitempty"`
	Note        string `json:"note"`
}

// Decide records the operator's answer to a pending boundary decision.
// The pause point honours it on the agent's next call; the answer is also
// recorded on the decision, so explain shows who answered and how.
func Decide(env Env, req DecideRequest) (Decided, error) {
	id := strings.TrimSpace(req.ID)
	if !domain.ValidDecisionID(id) {
		return Decided{}, actions.InputError{Err: fmt.Errorf("%q is not a boundary decision id (bnd-…); `tokenops boundaries status` lists the waiting ones", req.ID)}
	}
	var approved bool
	switch strings.ToLower(strings.TrimSpace(req.Decision)) {
	case "approve", "approved", "allow", "yes":
		approved = true
	case "deny", "denied", "no":
	default:
		return Decided{}, actions.InputError{Err: fmt.Errorf("decision %q is not approve or deny", req.Decision)}
	}
	method := req.Method
	if method == "" {
		method = MethodCLI
	}
	// The API approves only with a proof, which is checked below; MCP
	// never approves.
	if approved && !approvesInPerson(method) && (method != MethodAPI || req.Proof == nil) {
		return Decided{}, actions.InputError{Err: fmt.Errorf("an approval must come from a person: run `tokenops approve %s` in your own terminal, or answer Claude Code's prompt; this surface can only deny", id)}
	}
	p, ok, err := env.Dir.GetPending(id)
	if err != nil {
		return Decided{}, err
	}
	if !ok {
		return Decided{}, actions.InputError{Err: fmt.Errorf("no boundary decision %s is waiting on an answer", id)}
	}
	if approved && teamRouted(p) {
		role := strings.TrimPrefix(p.Require, "approver:")
		return Decided{}, actions.InputError{Err: fmt.Errorf("%s is your organisation's rule and needs approval from %s there: "+
			"it was sent to them, and they answer in the team plane's web view or with `tokenops team admin approve`; "+
			"you can still deny it here", p.Rule, role)}
	}
	scope := boundaryledger.ScopeOnce
	if req.Session {
		scope = boundaryledger.ScopeSession
	}
	var check presenceCheck
	if approved {
		if check, err = checkAnswerProof(env, p, scope, req.Proof, method); err != nil {
			return Decided{}, err
		}
	}
	now := env.now()
	a := boundaryledger.Answer{
		ID: id, Rule: p.Rule, SessionID: p.SessionID, Approved: approved, Scope: scope,
		Approver: env.user(), Method: method, At: now, Crossings: p.Crossings,
	}
	if check.presence == PresenceSigned {
		a.Proof = req.Proof
	}
	if err := env.Dir.PutAnswer(a); err != nil {
		return Decided{}, err
	}
	stage := eventschema.DecisionStageAccepted
	if !approved {
		stage = eventschema.DecisionStageRejected
	}
	if err := env.Dir.Append(answerEnvelope(p, stage, env, a, check)); err != nil {
		return Decided{}, err
	}
	out := Decided{ID: id, Rule: p.Rule, Decision: "approved", Scope: scope, Approver: a.Approver}
	if !approved {
		out.Decision = "denied"
	}
	out.Note = decidedNote(domain.Action(p.Action), approved, scope)
	if check.presence == PresenceSigned {
		out.Presence, out.PresenceKey = PresenceSigned, check.key
		out.Note += "; signed with presence key " + check.key + " (if you did not approve that dialog, `tokenops deny " + id + "`)"
	}
	return out, nil
}

// checkAnswerProof checks the proof an approval carries. A valid proof is
// recorded as signed. Without one, or with one that does not verify, the
// approval is refused when presence is enforced or it came over the API,
// with the reason and the way to give a signed one; under channel a CLI
// approval goes on as before, and the pause point records what the check
// would have said.
func checkAnswerProof(env Env, p boundaryledger.Pending, scope string, proof *presence.Proof, method string) (presenceCheck, error) {
	mode := env.approval()
	if proof == nil && !mode.Enforced() {
		return presenceCheck{}, nil
	}
	res := verifyAnswerProof(env, p, scope, proof)
	if res.OK() {
		return presenceCheck{presence: PresenceSigned, key: res.KeyID}, nil
	}
	if !mode.Enforced() && method != MethodAPI {
		return presenceCheck{presence: PresenceUnverified, key: res.KeyID, wouldRefuse: string(res.Reason)}, nil
	}
	why := "boundary_approval is " + string(mode)
	if !mode.Enforced() {
		why = "an approval over the API needs a valid presence proof"
	}
	if res.Reason == presence.ReasonNotEnrolled {
		return presenceCheck{}, actions.InputError{Err: fmt.Errorf("%s, and no presence key is enrolled on this machine: "+
			"enrol one with `%s`, or deny; `tokenops boundaries status` says what is set", why, EnrolCommand)}
	}
	return presenceCheck{}, actions.InputError{Err: fmt.Errorf("%s, and this approval is not honoured: %s. "+
		"`tokenops approve %s` in your own terminal asks for Touch ID, a paired Apple Watch or your password and signs it; "+
		"denying needs no signature", why, res.Reason.Phrase(), p.ID)}
}

// Waiting reads one decision waiting on an answer, for a person about to
// give it: `tokenops approve --confirm` shows it before asking.
func Waiting(env Env, id string) (PendingView, error) {
	id = strings.TrimSpace(id)
	if !domain.ValidDecisionID(id) {
		return PendingView{}, actions.InputError{Err: fmt.Errorf("%q is not a boundary decision id (bnd-…)", id)}
	}
	p, ok, err := env.Dir.GetPending(id)
	if err != nil {
		return PendingView{}, err
	}
	if !ok {
		return PendingView{}, actions.InputError{Err: fmt.Errorf("no boundary decision %s is waiting on an answer", id)}
	}
	return PendingView{
		ID: p.ID, Rule: p.Rule, Require: p.Require, Crossed: p.Crossed, Harness: p.Harness,
		SessionID: p.SessionID, At: p.At, Approve: "tokenops approve " + p.ID,
	}, nil
}

// approvesInPerson reports whether an answer by this method can approve.
// The agent a boundary stopped can call MCP tools and reach the local API,
// so those may only deny; an approval comes from a terminal (the CLI
// refuses one without a TTY) or from the harness's own permission prompt.
func approvesInPerson(method string) bool {
	return method == MethodCLI || method == MethodPrompt
}

func decidedNote(action domain.Action, approved bool, scope string) string {
	if !agentFacing(action) {
		what := "the next upload or export"
		if action == domain.ActionModelRoute {
			what = "the route"
		}
		switch {
		case approved && scope == boundaryledger.ScopeSession:
			return what + " goes through for the next 24 hours"
		case approved:
			return what + " goes through once, and is asked about again after that"
		case scope == boundaryledger.ScopeSession:
			return what + " stays held for the next 24 hours"
		default:
			return what + " stays held"
		}
	}
	switch {
	case approved && scope == boundaryledger.ScopeSession:
		return "the agent may go on for the rest of this session; tell it to continue"
	case approved:
		return "the agent may take its next step; tell it to continue, and it is asked again after that"
	case scope == boundaryledger.ScopeSession:
		return "the agent is refused for the rest of this session"
	default:
		return "the agent's next step is refused"
	}
}

// answerEnvelope records an answer on the decision it answers, with what
// the presence check found for an approval.
func answerEnvelope(p boundaryledger.Pending, stage eventschema.DecisionStage, env Env, a boundaryledger.Answer, check presenceCheck) *eventschema.Envelope {
	rule := domain.Rule{Name: p.Rule, Action: domain.Action(p.Action)}
	if req, err := domain.ParseRequire(p.Require); err == nil {
		rule.Require = req
	}
	rule.Stage = domain.StageEnforce
	call := Call{Harness: p.Harness, SessionID: p.SessionID, AgentID: p.AgentID, Action: domain.Action(p.Action)}
	m := domain.RecordedMatch(rule, p.Crossed)
	return decisionEnvelope(m, stage, call, env, a.At, nil, check.stamp(eventschema.DecisionApproval{
		Approver: a.Approver, Method: a.Method, At: a.At, Rule: a.Rule, Scope: a.Scope,
	}))
}
