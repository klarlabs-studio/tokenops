package actions

import (
	"context"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"strings"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/outcomes"
	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/contexts/boundaries/presence"
	"go.klarlabs.de/tokenops/internal/contexts/optimization/routingapproval"
	infrapresence "go.klarlabs.de/tokenops/internal/infra/presence"
	"go.klarlabs.de/tokenops/internal/storage/sqlite"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// PreferredModels is every provider's preferred model after a change.
type PreferredModels struct {
	PreferredModels map[string]string `json:"preferred_models"`
	Config          string            `json:"config"`
}

// SetPreferredModel sets the model a provider's routing may not move
// above, or clears it. A route to a pricier model is then referred to the
// operator rather than applied.
func SetPreferredModel(path, provider, model string, clear bool) (PreferredModels, error) {
	provider, model = strings.TrimSpace(provider), strings.TrimSpace(model)
	if provider == "" {
		return PreferredModels{}, inputErr(errors.New("provider is required"))
	}
	if !clear && model == "" {
		return PreferredModels{}, inputErr(errors.New("model is required unless clear=true"))
	}
	cfg, err := edit(path, func(c *config.Config) error {
		if clear {
			delete(c.PreferredModels, provider)
			return nil
		}
		if c.PreferredModels == nil {
			c.PreferredModels = map[string]string{}
		}
		c.PreferredModels[provider] = model
		return nil
	})
	if err != nil {
		return PreferredModels{}, err
	}
	return PreferredModels{PreferredModels: cfg.PreferredModels, Config: path}, nil
}

// RoutingDecision is the operator's answer to a routing proposal.
type RoutingDecision struct {
	Key      string `json:"key"`
	Decision string `json:"decision"`
	Model    string `json:"model"`
	// Presence is signed when the approval carried a valid presence proof,
	// with the key it was signed by.
	Presence    string `json:"presence,omitempty"`
	PresenceKey string `json:"presence_key,omitempty"`
	Note        string `json:"note"`
}

// DecideRouting records the operator's answer to the proposal key in the
// approval store at storePath (empty: the default), as a terminal answer
// under boundary_approval: channel. Surfaces that know their setting and
// channel call AnswerRouting.
func DecideRouting(storePath, key, decision string) (RoutingDecision, error) {
	return AnswerRouting(RoutingAnswer{StorePath: storePath, Key: key, Decision: decision, Method: RoutingMethodCLI})
}

// How a routing answer arrived.
const (
	RoutingMethodCLI = "cli"
	RoutingMethodAPI = "api"
	RoutingMethodMCP = "mcp"
)

// RoutingAnswer is the operator's answer to a routing proposal.
type RoutingAnswer struct {
	// StorePath is the approval log; empty is the default.
	StorePath string
	Key       string
	// Decision is approve or deny.
	Decision string
	// Approval is boundary_approval: with presence enforced, an upgrade
	// approval counts only with a valid Proof (ADR 0015 §3), and MCP
	// cannot approve one.
	Approval presence.Mode
	Proof    *presence.Proof
	Method   string
}

// AnswerRouting records the operator's answer. approve routes future
// matching requests to the proposed model; deny keeps the requested one.
// It applies from the next request, with no restart. A deny needs no
// proof.
func AnswerRouting(req RoutingAnswer) (RoutingDecision, error) {
	key := strings.TrimSpace(req.Key)
	if key == "" {
		return RoutingDecision{}, inputErr(errors.New("key is required (from the pending routing proposals)"))
	}
	store, err := openRoutingStore(req.StorePath)
	if err != nil {
		return RoutingDecision{}, err
	}
	state, err := store.Load()
	if err != nil {
		return RoutingDecision{}, err
	}
	st, ok := state[key]
	if !ok {
		return RoutingDecision{}, inputErr(errors.New("no routing proposal with key " + key))
	}
	var chosen, decision string
	switch strings.ToLower(strings.TrimSpace(req.Decision)) {
	case "approve", "approved":
		decision, chosen = "approved", st.To
	case "deny", "denied":
		decision, chosen = "denied", st.From
	default:
		return RoutingDecision{}, inputErr(errors.New(`decision must be "approve" or "deny"`))
	}
	var proof *presence.Proof
	out := RoutingDecision{Key: key, Decision: decision, Model: chosen,
		Note: "applies to matching requests from now on; no restart needed"}
	if decision == "approved" {
		if proof, err = checkRoutingProof(store, st, req); err != nil {
			return RoutingDecision{}, err
		}
		if proof != nil {
			out.Presence, out.PresenceKey = "signed", proof.Key
			out.Note += "; signed with presence key " + proof.Key
		}
	}
	if err := store.DecideSigned(key, decision, chosen, proof); err != nil {
		return RoutingDecision{}, err
	}
	return out, nil
}

func openRoutingStore(path string) (*routingapproval.Store, error) {
	if path == "" {
		p, err := routingapproval.DefaultPath()
		if err != nil {
			return nil, err
		}
		path = p
	}
	return routingapproval.Open(path)
}

// checkRoutingProof checks an upgrade approval's proof: a valid one is
// kept on the decision; without one the approval goes on under channel and
// is refused under presence, where MCP cannot approve at all.
func checkRoutingProof(store *routingapproval.Store, st routingapproval.State, req RoutingAnswer) (*presence.Proof, error) {
	cmd := "tokenops routing decide " + shellQuote(st.Key) + " approve"
	if req.Approval.Enforced() && req.Method == RoutingMethodMCP {
		return nil, inputErr(fmt.Errorf("boundary_approval is %s: a model upgrade is approved in person, not through an agent; "+
			"run `%s` in your own terminal (it asks for Touch ID, a paired Apple Watch or your password), or deny here", req.Approval, cmd))
	}
	if req.Proof == nil && !req.Approval.Enforced() {
		return nil, nil
	}
	res := VerifyRoutingProof(store, st, req.Proof, time.Now().UTC())
	if res.OK() {
		return req.Proof, nil
	}
	if !req.Approval.Enforced() {
		return nil, nil
	}
	if res.Reason == presence.ReasonNotEnrolled {
		return nil, inputErr(fmt.Errorf("boundary_approval is %s, and no presence key is enrolled on this machine: "+
			"enrol one with `tokenops boundaries presence enroll`, or deny", req.Approval))
	}
	return nil, inputErr(fmt.Errorf("boundary_approval is %s, and this approval is not honoured: %s. `%s` in your own terminal signs it; "+
		"denying needs no signature", req.Approval, res.Reason.Phrase(), cmd))
}

// VerifyRoutingProof checks a routing upgrade approval against the
// enrolled presence key, as of now: the decision's time when the router
// re-checks a recorded one, since a routing answer lasts.
func VerifyRoutingProof(store *routingapproval.Store, st routingapproval.State, proof *presence.Proof, now time.Time) presence.Result {
	trust, err := routingPresenceDir(store).Trust(time.Now().UTC())
	if err != nil {
		return presence.Result{Reason: presence.ReasonKeyUnreadable}
	}
	return presence.Verify(proof, presence.RoutingExpect(st.Key, st.RaisedAt, now), trust)
}

// routingPresenceDir is the presence key store beside the approval log
// (~/.tokenops/presence).
func routingPresenceDir(store *routingapproval.Store) infrapresence.Dir {
	return infrapresence.Dir(filepath.Join(filepath.Dir(store.Path()), "presence"))
}

// RoutingSigner signs with the presence key; the presence helper
// satisfies it.
type RoutingSigner interface {
	Sign(ctx context.Context, blobPath string, msg []byte, reason string) ([]byte, error)
}

// SignRoutingApproval asks the person at this Mac to sign an approval of
// the routing upgrade key; the dialog names the provider and both models.
func SignRoutingApproval(ctx context.Context, storePath, key string, s RoutingSigner) (*presence.Proof, error) {
	store, err := openRoutingStore(storePath)
	if err != nil {
		return nil, err
	}
	state, err := store.Load()
	if err != nil {
		return nil, err
	}
	st, ok := state[strings.TrimSpace(key)]
	if !ok {
		return nil, inputErr(errors.New("no routing proposal with key " + key))
	}
	dir := routingPresenceDir(store)
	now := time.Now().UTC()
	trust, err := dir.Trust(now)
	if err != nil {
		return nil, err
	}
	switch {
	case trust.State == presence.TrustNotEnrolled:
		return nil, inputErr(errors.New("no presence key is enrolled on this machine: enrol one with `tokenops boundaries presence enroll`"))
	case trust.State == presence.TrustHeld:
		return nil, inputErr(errors.New("approvals are held until " + trust.Pin.HoldUntil.Local().Format("15:04") +
			": the presence key was replaced without the old key's signature"))
	case !dir.HasBlob():
		return nil, inputErr(errors.New("the presence key is enrolled but its key blob is not on this machine: " +
			"re-enrol with `tokenops boundaries presence enroll --replace`"))
	}
	e := presence.RoutingExpect(st.Key, st.RaisedAt, now)
	proof := presence.Proof{
		V: presence.Version, Key: trust.Key.ID, Decision: st.Key, Rule: presence.RoutingRule, Scope: e.Scope,
		Challenge: e.Challenge, Crossed: presence.CrossingsDigest(nil), Issued: now.Truncate(time.Second),
	}
	msg, err := proof.Message()
	if err != nil {
		return nil, err
	}
	reason := fmt.Sprintf("approve a TokenOps model upgrade for %s requests from %s to %s", st.Provider, st.From, st.To)
	if proof.Sig, err = s.Sign(ctx, dir.BlobPath(), msg, reason); err != nil {
		return nil, err
	}
	if res := presence.Verify(&proof, e, trust); !res.OK() {
		return nil, fmt.Errorf("the signature the presence helper returned is not valid (%s)", res.Reason.Phrase())
	}
	return &proof, nil
}

// shellQuote quotes a routing key for a command the operator pastes: it
// holds | and may hold other shell characters.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// OutcomeRequest is the operator's judgement of one execution.
type OutcomeRequest struct {
	ExecutionID string
	DecisionID  string
	// Result is achieved, partial or not_achieved.
	Result string
	Caveat string
	// AttentionMinutes is active human effort, only when the operator
	// reported or confirmed it.
	AttentionMinutes *float64
}

// Outcome is a recorded judgement.
type Outcome struct {
	EventID     string `json:"event_id,omitempty"`
	ExecutionID string `json:"execution_id"`
	Result      string `json:"result"`
	Assessment  string `json:"assessment"`
	Recorded    bool   `json:"recorded"`
	Error       string `json:"error,omitempty"`
	Hint        string `json:"hint,omitempty"`
}

// RecordOutcome stores the operator's judgement of an execution, linked to
// its decision when one is named. A task ending or an agent saying done is
// not an outcome; only the operator's judgement is.
func RecordOutcome(ctx context.Context, store *sqlite.Store, req OutcomeRequest) (Outcome, error) {
	if strings.TrimSpace(req.ExecutionID) == "" {
		return Outcome{}, inputErr(errors.New("execution_id is required"))
	}
	result, err := ParseOutcomeResult(req.Result)
	if err != nil {
		return Outcome{}, inputErr(err)
	}
	if m := req.AttentionMinutes; m != nil && (math.IsNaN(*m) || math.IsInf(*m, 0) || *m < 0) {
		return Outcome{}, inputErr(errors.New("attention_minutes must be a finite non-negative number"))
	}
	out := Outcome{ExecutionID: req.ExecutionID, Result: string(result), Assessment: string(eventschema.OutcomeHuman)}
	if store == nil {
		out.Error, out.Hint = "storage_disabled", "run `tokenops init` then restart the daemon"
		return out, nil
	}
	env := outcomes.Event(outcomes.Record{
		ExecutionID: req.ExecutionID, DecisionID: req.DecisionID,
		Result: result, Assessment: eventschema.OutcomeHuman, Caveat: strings.TrimSpace(req.Caveat),
		AttentionMinutes: req.AttentionMinutes,
	})
	if err := CorrelateOutcome(ctx, store, env); err != nil {
		return Outcome{}, err
	}
	if err := store.Append(ctx, env); err != nil {
		return Outcome{}, err
	}
	out.EventID, out.Recorded = env.ID, true
	return out, nil
}

// CorrelateOutcome links an outcome to its decision's lifecycle.
func CorrelateOutcome(ctx context.Context, store *sqlite.Store, env *eventschema.Envelope) error {
	if env.Correlation.Decision == "" {
		return nil
	}
	history, err := store.Query(ctx, sqlite.Filter{Decision: env.Correlation.Decision, Limit: 10_000})
	if err != nil {
		return err
	}
	outcomes.CorrelateDecisionLifecycle(env, history)
	return nil
}

// ParseOutcomeResult reads achieved, partial or not_achieved.
func ParseOutcomeResult(raw string) (eventschema.OutcomeResult, error) {
	switch r := eventschema.OutcomeResult(strings.ToLower(strings.TrimSpace(raw))); r {
	case eventschema.OutcomeAchieved, eventschema.OutcomePartial, eventschema.OutcomeNotAchieved:
		return r, nil
	default:
		return eventschema.OutcomeUnknown, fmt.Errorf("result must be achieved, partial, or not_achieved")
	}
}
