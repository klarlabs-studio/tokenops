package teamwire

import (
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"slices"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/boundaries"
)

// This file is the boundaries half of the contract (ADR 0014 §4–5): the
// organisation's rules, which travel from the server to members' machines,
// and approval requests, which travel the other way. A request carries
// enumerations, figures and the rule's own name — never a prompt, a path,
// a model's output or a name a member typed: TestApprovalHasNoFreeText
// holds it to that, as TestUploadHasNoFreeText holds the upload.

// BoundaryRule is one organisation boundary as an owner or admin wrote
// it, in the shape config.yaml's boundaries section takes.
type BoundaryRule struct {
	Name    string            `json:"name"`
	Action  string            `json:"action"`
	When    map[string]string `json:"when,omitempty"`
	Require string            `json:"require"`
	// Stage is shadow or enforce, set by the organisation: a member can
	// neither promote nor demote an organisation rule.
	Stage string `json:"stage,omitempty"`
}

// ValidateBoundaries checks rules exactly as a member's machine parses
// them, and that every approver is a role the team plane has.
func ValidateBoundaries(rules []BoundaryRule) error {
	parsed, err := boundaries.Parse(specs(rules))
	if err != nil {
		return err
	}
	for _, r := range parsed {
		if r.Require.Kind == boundaries.RequireApprover && !ValidRole(r.Require.Role) {
			return fmt.Errorf("rule %q: approver %q is not a role (%v)", r.Name, r.Require.Role, Roles)
		}
	}
	return nil
}

func specs(rules []BoundaryRule) []boundaries.RuleSpec {
	out := make([]boundaries.RuleSpec, len(rules))
	for i, r := range rules {
		out[i] = boundaries.RuleSpec{Name: r.Name, Action: r.Action, When: r.When, Require: r.Require, Stage: r.Stage}
	}
	return out
}

// OrgBoundaries is the organisation's rules at one version.
type OrgBoundaries struct {
	OrgID string `json:"org_id"`
	// Version increases with every change, so a machine never takes an
	// older document for a newer one.
	Version   int64          `json:"version"`
	UpdatedAt time.Time      `json:"updated_at"`
	Rules     []BoundaryRule `json:"rules"`
}

// Parsed is the document's rules as a member's machine applies them:
// marked as the organisation's, names prefixed org:.
func (d OrgBoundaries) Parsed() ([]boundaries.Rule, error) {
	rules, err := boundaries.Parse(specs(d.Rules))
	if err != nil {
		return nil, err
	}
	return boundaries.FromOrg(rules), nil
}

// SignedBoundaries is OrgBoundaries as the server delivers it: the
// document's exact bytes, the server's Ed25519 signature over them, and
// the public key. A machine pins the key the first time and checks every
// document against it — the cached copy too, each time it is read — so
// rules edited on disk are not rules.
type SignedBoundaries struct {
	Document  []byte `json:"document"`
	Signature []byte `json:"signature"`
	Key       []byte `json:"key"`
}

// SignBoundaries signs doc.
func SignBoundaries(doc OrgBoundaries, key ed25519.PrivateKey) (SignedBoundaries, error) {
	b, err := json.Marshal(doc)
	if err != nil {
		return SignedBoundaries{}, err
	}
	pub, ok := key.Public().(ed25519.PublicKey)
	if !ok {
		return SignedBoundaries{}, errors.New("not an Ed25519 key")
	}
	return SignedBoundaries{Document: b, Signature: ed25519.Sign(key, b), Key: pub}, nil
}

// ErrBoundaryKeyChanged is a document signed with a key other than the
// one this machine pinned.
var ErrBoundaryKeyChanged = errors.New("the organisation's rules are signed with a key this machine does not trust")

// Verify checks the signature against pinned, or, with none pinned yet,
// against the key the document names, and returns the document.
func (s SignedBoundaries) Verify(pinned ed25519.PublicKey) (OrgBoundaries, error) {
	key := ed25519.PublicKey(s.Key)
	if len(pinned) > 0 {
		if !pinned.Equal(key) {
			return OrgBoundaries{}, ErrBoundaryKeyChanged
		}
		key = pinned
	}
	if len(key) != ed25519.PublicKeySize || !ed25519.Verify(key, s.Document, s.Signature) {
		return OrgBoundaries{}, errors.New("the organisation's rules do not carry a valid signature")
	}
	var doc OrgBoundaries
	if err := json.Unmarshal(s.Document, &doc); err != nil {
		return OrgBoundaries{}, fmt.Errorf("organisation rules: %w", err)
	}
	return doc, nil
}

// Harnesses that ask for an approval: the clients whose hooks refuse, and
// TokenOps' own decision points.
var Harnesses = []string{"claude-code", "opencode", "codex", "cursor", "router", "tokenops"}

// Approval states.
const (
	ApprovalPending  = "pending"
	ApprovalApproved = "approved"
	ApprovalDenied   = "denied"
	// ApprovalExpired is a request nobody answered within a day.
	ApprovalExpired = "expired"
)

// Approval scopes: the next step, or the rest of the session (a route or
// a destination: 24 hours).
const (
	ScopeOnce    = "once"
	ScopeSession = "session"
)

// Crossing is one figure that crossed a rule's condition.
type Crossing struct {
	// Field is the condition: session_cost_usd, plan_left_pct,
	// budget_used_pct, model_tier or destination.
	Field string `json:"field"`
	// Op is the comparator: <, <=, >, >= or ==.
	Op string `json:"op"`
	// Value is the figure and Threshold the rule's; a model tier is its
	// rank (haiku 1, sonnet 2, opus 3).
	Value     float64 `json:"value"`
	Threshold float64 `json:"threshold"`
	// Window is 5h or week for plan_left_pct; Destination team or otel.
	Window      string `json:"window,omitempty"`
	Destination string `json:"destination,omitempty"`
}

var (
	crossingFields = []string{"session_cost_usd", "plan_left_pct", "budget_used_pct", "model_tier", "destination"}
	crossingOps    = []string{"<", "<=", ">", ">=", "=="}
	crossingWins   = []string{"", "5h", "week"}
	crossingDests  = []string{"", "team", "otel"}
	decisionID     = regexp.MustCompile(`^bnd-[0-9a-f]{12}$`)
)

// ValidDecisionID reports whether s is a boundary decision's id.
func ValidDecisionID(s string) bool { return decisionID.MatchString(s) }

// Validate checks every field against its enumeration or range.
func (c Crossing) Validate() error {
	switch {
	case !slices.Contains(crossingFields, c.Field):
		return fmt.Errorf("crossing field %q", c.Field)
	case !slices.Contains(crossingOps, c.Op):
		return fmt.Errorf("crossing op %q", c.Op)
	case !slices.Contains(crossingWins, c.Window):
		return fmt.Errorf("crossing window %q", c.Window)
	case !slices.Contains(crossingDests, c.Destination):
		return fmt.Errorf("crossing destination %q", c.Destination)
	case !finite(c.Value) || !finite(c.Threshold):
		return errors.New("crossing figures must be finite")
	}
	return nil
}

func finite(f float64) bool { return !math.IsNaN(f) && !math.IsInf(f, 0) }

// ApprovalRequest asks the organisation for an approval a rule requires:
// sent by a member's machine when an organisation rule naming an approver
// holds an action.
type ApprovalRequest struct {
	// DecisionID is the machine's decision id; a retry of the same
	// decision is the same request.
	DecisionID string `json:"decision_id"`
	// Rule is the organisation rule's name, without the org: prefix, and
	// Version the rules' version it was decided under.
	Rule    string `json:"rule"`
	Version int64  `json:"version"`
	Action  string `json:"action"`
	Harness string `json:"harness"`
	// Crossings are the figures that crossed the rule.
	Crossings []Crossing `json:"crossings"`
}

// maxCrossings bounds a request; a rule has at most one condition per field.
const maxCrossings = 8

// Validate checks the request is enumerations and figures only.
func (a ApprovalRequest) Validate() error {
	switch {
	case !ValidDecisionID(a.DecisionID):
		return fmt.Errorf("decision id %q", a.DecisionID)
	case !boundaries.ValidName(a.Rule):
		return fmt.Errorf("rule %q", a.Rule)
	case a.Version < 1:
		return errors.New("version must be positive")
	case !slices.Contains(boundaryActions(), a.Action):
		return fmt.Errorf("action %q", a.Action)
	case !slices.Contains(Harnesses, a.Harness):
		return fmt.Errorf("harness %q", a.Harness)
	case len(a.Crossings) > maxCrossings:
		return fmt.Errorf("%d crossings; at most %d", len(a.Crossings), maxCrossings)
	}
	for _, c := range a.Crossings {
		if err := c.Validate(); err != nil {
			return err
		}
	}
	return nil
}

func boundaryActions() []string {
	return []string{
		string(boundaries.ActionContinue), string(boundaries.ActionSpend), string(boundaries.ActionSubagentModel),
		string(boundaries.ActionModelRoute), string(boundaries.ActionShare),
	}
}

// Approval is a request as the server holds it, for the machine that
// asked and for the people who may answer it.
type Approval struct {
	ID         string     `json:"id"`
	DecisionID string     `json:"decision_id"`
	Rule       string     `json:"rule"`
	Action     string     `json:"action"`
	Harness    string     `json:"harness"`
	Role       string     `json:"role"`
	Crossings  []Crossing `json:"crossings"`
	// Member is who asked, by display name.
	Member      string    `json:"member,omitempty"`
	RequestedAt time.Time `json:"requested_at"`
	Status      string    `json:"status"`
	Scope       string    `json:"scope,omitempty"`
	// AnsweredBy is who answered, by display name.
	AnsweredBy string     `json:"answered_by,omitempty"`
	AnsweredAt *time.Time `json:"answered_at,omitempty"`
}

// ApprovalAnswer is an approver's answer.
type ApprovalAnswer struct {
	// Decision is approve or deny.
	Decision string `json:"decision"`
	// Session makes the answer hold for the rest of the session (24 hours
	// for a route or a destination) rather than the next step.
	Session bool `json:"session,omitempty"`
}
