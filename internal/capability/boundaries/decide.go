package boundaries

import (
	"fmt"
	"strings"

	"go.klarlabs.de/tokenops/internal/capability/actions"
	domain "go.klarlabs.de/tokenops/internal/contexts/boundaries"
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
	Session bool   `json:"session,omitempty"`
	Method  string `json:"-"`
}

// Decided is what an answer did.
type Decided struct {
	ID       string `json:"id"`
	Rule     string `json:"rule"`
	Decision string `json:"decision"`
	Scope    string `json:"scope"`
	Approver string `json:"approver"`
	Note     string `json:"note"`
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
	p, ok, err := env.Dir.GetPending(id)
	if err != nil {
		return Decided{}, err
	}
	if !ok {
		return Decided{}, actions.InputError{Err: fmt.Errorf("no boundary decision %s is waiting on an answer", id)}
	}
	scope := boundaryledger.ScopeOnce
	if req.Session {
		scope = boundaryledger.ScopeSession
	}
	method := req.Method
	if method == "" {
		method = MethodCLI
	}
	now := env.now()
	a := boundaryledger.Answer{
		ID: id, Rule: p.Rule, SessionID: p.SessionID, Approved: approved, Scope: scope,
		Approver: env.user(), Method: method, At: now,
	}
	if err := env.Dir.PutAnswer(a); err != nil {
		return Decided{}, err
	}
	stage := eventschema.DecisionStageAccepted
	if !approved {
		stage = eventschema.DecisionStageRejected
	}
	if err := env.Dir.Append(answerEnvelope(p, stage, env, a)); err != nil {
		return Decided{}, err
	}
	out := Decided{ID: id, Rule: p.Rule, Decision: "approved", Scope: scope, Approver: a.Approver}
	if !approved {
		out.Decision = "denied"
	}
	out.Note = decidedNote(approved, scope)
	return out, nil
}

func decidedNote(approved bool, scope string) string {
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

// answerEnvelope records an answer on the decision it answers.
func answerEnvelope(p boundaryledger.Pending, stage eventschema.DecisionStage, env Env, a boundaryledger.Answer) *eventschema.Envelope {
	rule := domain.Rule{Name: p.Rule, Action: domain.Action(p.Action)}
	if req, err := domain.ParseRequire(p.Require); err == nil {
		rule.Require = req
	}
	rule.Stage = domain.StageEnforce
	call := Call{Harness: p.Harness, SessionID: p.SessionID, AgentID: p.AgentID, Action: domain.Action(p.Action)}
	m := domain.RecordedMatch(rule, p.Crossed)
	return decisionEnvelope(m, stage, call, env, a.At, nil, eventschema.DecisionApproval{
		Approver: a.Approver, Method: a.Method, At: a.At, Rule: a.Rule, Scope: a.Scope,
	})
}
