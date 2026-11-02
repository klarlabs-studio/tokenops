package boundaries

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"go.klarlabs.de/tokenops/internal/capability/actions"
	"go.klarlabs.de/tokenops/internal/capability/explain"
	domain "go.klarlabs.de/tokenops/internal/contexts/boundaries"
	"go.klarlabs.de/tokenops/internal/contexts/boundaries/presence"
	"go.klarlabs.de/tokenops/internal/infra/boundaryledger"
	infrapresence "go.klarlabs.de/tokenops/internal/infra/presence"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// MaxExtra is the longest extra allowance one grant gives: the same day
// a session-long approval is honoured for. A longer lift is a rule
// change, made in config.yaml.
const MaxExtra = 24 * time.Hour

// MethodExtra is how a decision the extra allowance let through records
// its approval.
const MethodExtra = "extra"

// ExtraRequest is the operator granting themselves a temporary extra
// allowance against boundaries: they do not bind until it ends.
type ExtraRequest struct {
	// For is how long it lasts from now; Until when it ends. Exactly one
	// is set.
	For   time.Duration
	Until time.Time
	// Rules are the boundaries it lifts; empty lifts every local rule
	// that pauses on plan headroom (the headroom pause).
	Rules []string
	// Why is the operator's reason, recorded on the decision.
	Why string
	// Method is how the grant reached TokenOps; only the CLI grants.
	Method string
	// Signer signs the grant with this Mac's presence key when
	// boundary_approval is presence or presence-strict (ADR 0015, "Extra
	// allowances"); unused otherwise.
	Signer Signer
}

// ExtraView is an extra allowance as status, the glance and the API show
// it.
type ExtraView struct {
	ID      string    `json:"id"`
	Rules   []string  `json:"rules"`
	At      time.Time `json:"at"`
	Until   time.Time `json:"until"`
	Granter string    `json:"granter"`
	Why     string    `json:"why,omitempty"`
	// Presence is "signed" when the grant carries a valid presence-key
	// signature, and "refused" when presence is enforced and it does not,
	// so it lifts nothing; empty for an unsigned grant under channel.
	Presence string `json:"presence,omitempty"`
	// PresenceKey is the key that signed it.
	PresenceKey string `json:"presence_key,omitempty"`
	// Ignored says why a refused grant lifts nothing.
	Ignored string `json:"ignored,omitempty"`
}

func extraView(e boundaryledger.Extra) ExtraView {
	return ExtraView{ID: e.ID, Rules: e.Rules, At: e.At, Until: e.Until, Granter: e.Granter, Why: e.Why}
}

// Lifts reports whether the extra allowance lifts its boundaries: every
// one does under channel, and under presence only a signed one.
func (v ExtraView) Lifts() bool { return v.Presence != PresenceRefused }

// ExtraGranted is what a grant did.
type ExtraGranted struct {
	ExtraView
	Note string `json:"note"`
}

// GrantExtra records an extra allowance. It is the operator's word only:
// no MCP tool or API route reaches it, and the CLI refuses it without a
// terminal. While approvals must be signed (boundary_approval: presence
// or presence-strict) the grant is signed with this Mac's presence key
// through req.Signer, which raises the dialog; without a key, a helper or
// a person at the screen it is refused, and the pause point honours it
// only while the signature verifies. The grant is recorded as a decision
// with who granted it, why and until when, and it ends on its own.
func GrantExtra(ctx context.Context, rules []domain.Rule, env Env, req ExtraRequest) (ExtraGranted, error) {
	if req.Method != MethodCLI {
		return ExtraGranted{}, actions.InputError{Err: fmt.Errorf("an extra allowance is the operator's word: " +
			"run `tokenops boundaries extra` in your own terminal; an agent cannot grant one")}
	}
	var trust presence.Trust
	if env.approval().Enforced() {
		var err error
		if trust, err = grantTrust(env, req.Signer); err != nil {
			return ExtraGranted{}, err
		}
	}
	now := env.now()
	until, err := extraUntil(req, now)
	if err != nil {
		return ExtraGranted{}, err
	}
	names, err := extraRules(rules, req.Rules)
	if err != nil {
		return ExtraGranted{}, err
	}
	e := boundaryledger.Extra{
		ID: domain.DecisionID("_extra", uuid.NewString()), Rules: names,
		At: now, Until: until, Granter: env.user(), Method: req.Method, Why: strings.TrimSpace(req.Why),
	}
	if env.approval().Enforced() {
		if err := signExtra(ctx, env, req.Signer, trust, &e); err != nil {
			return ExtraGranted{}, err
		}
	}
	if err := env.Dir.PutExtra(e, now); err != nil {
		return ExtraGranted{}, err
	}
	if err := env.Dir.Append(extraEnvelope(e, eventschema.DecisionStageAccepted, now)); err != nil {
		return ExtraGranted{}, err
	}
	view := extraView(e)
	note := fmt.Sprintf("%s will not ask or refuse until %s; then %s again on its own. "+
		"`tokenops boundaries extra --end` ends it sooner",
		strings.Join(names, ", "), e.Until.Local().Format("15:04"), bindWord(len(names)))
	if e.Grant != nil {
		view.Presence, view.PresenceKey = PresenceSigned, e.Grant.Key
		note += "; signed with presence key " + e.Grant.Key + ", and honoured only while that signature verifies"
	}
	return ExtraGranted{ExtraView: view, Note: note}, nil
}

// grantTrust is the key a grant is signed under when presence is
// enforced: refused, wrapping ErrPresenceUnavailable, when there is no
// helper or no usable key.
func grantTrust(env Env, s Signer) (presence.Trust, error) {
	if s == nil {
		return presence.Trust{}, extraUnavailable(env, unavailable{"the presence helper is not installed on this machine"})
	}
	trust, err := signingTrust(env)
	if err != nil {
		return presence.Trust{}, extraUnavailable(env, err)
	}
	return trust, nil
}

func extraUnavailable(env Env, err error) error {
	return fmt.Errorf("boundary_approval is %s, so an extra allowance must be signed with this Mac's presence key, "+
		"and it cannot be: %w; nothing was granted", env.approval(), err)
}

// signExtra has the person at this Mac sign the grant (one dialog naming
// what it lifts and until when) and attaches the signature. The grant's
// times are whole seconds, as the statement binds them. The signature is
// checked before the grant is stored, so a blob that does not match the
// enrolled key is caught here, not by a grant that silently lifts nothing.
func signExtra(ctx context.Context, env Env, s Signer, trust presence.Trust, e *boundaryledger.Extra) error {
	e.At, e.Until = e.At.UTC().Truncate(time.Second), e.Until.UTC().Truncate(time.Second)
	g := presence.Grant{
		V: presence.Version, Key: trust.Key.ID, ID: e.ID, Rules: e.Rules,
		Granter: e.Granter, Why: e.Why, At: e.At, Until: e.Until,
	}
	msg, err := g.Message()
	if err != nil {
		return err
	}
	if g.Sig, err = s.Sign(ctx, env.presenceDir().BlobPath(), msg, infrapresence.DialogText(extraDialog(*e))); err != nil {
		if err = signError(err); errors.Is(err, ErrPresenceUnavailable) {
			return extraUnavailable(env, err)
		}
		return fmt.Errorf("%w: nothing was granted", err)
	}
	if res := presence.VerifyGrant(&g, grantExpect(*e, env.now()), trust); !res.OK() {
		return fmt.Errorf("the signature the presence helper returned is not valid (%s): re-enrol with `%s --replace`",
			res.Reason.Phrase(), EnrolCommand)
	}
	e.Grant = &g
	return nil
}

// extraDialog is the dialog's text: what the grant lifts, until when, and
// why, because a paired Apple Watch shows only this.
func extraDialog(e boundaryledger.Extra) string {
	text := fmt.Sprintf("grant a TokenOps extra allowance lifting %s until %s",
		strings.Join(e.Rules, ", "), e.Until.Local().Format("Mon 15:04"))
	if e.Why != "" {
		text += " (" + e.Why + ")"
	}
	return text
}

func grantExpect(e boundaryledger.Extra, now time.Time) presence.GrantExpect {
	return presence.GrantExpect{ID: e.ID, Rules: e.Rules, Granter: e.Granter, Why: e.Why, At: e.At, Until: e.Until, Now: now}
}

// checkExtra is the presence check on one stored grant.
func checkExtra(e boundaryledger.Extra, trust presence.Trust, trustErr error, now time.Time) presence.Result {
	if trustErr != nil {
		return presence.Result{Reason: presence.ReasonKeyUnreadable}
	}
	return presence.VerifyGrant(e.Grant, grantExpect(e, now), trust)
}

// honouredExtras is the extra allowances that lift boundaries at now:
// every active one under channel; under presence, only those whose
// signature verifies, so a hand-written or edited extras.json lifts
// nothing. The key store is read only when an extra is active.
func honouredExtras(env Env, now time.Time) []boundaryledger.Extra {
	active := env.Dir.ActiveExtras(now)
	if len(active) == 0 || !env.approval().Enforced() {
		return active
	}
	trust, err := env.presenceDir().Trust(now)
	return slices.DeleteFunc(active, func(e boundaryledger.Extra) bool { return !checkExtra(e, trust, err, now).OK() })
}

func bindWord(n int) string {
	if n == 1 {
		return "it binds"
	}
	return "they bind"
}

// extraUntil is when the grant ends, at most MaxExtra from now.
func extraUntil(req ExtraRequest, now time.Time) (time.Time, error) {
	switch {
	case req.For > 0 && !req.Until.IsZero():
		return time.Time{}, actions.InputError{Err: fmt.Errorf("give a duration or --until, not both")}
	case req.For > 0:
		req.Until = now.Add(req.For)
	case req.Until.IsZero():
		return time.Time{}, actions.InputError{Err: fmt.Errorf("say how long: a duration such as 30m, or --until 18:00")}
	}
	if !req.Until.After(now) {
		return time.Time{}, actions.InputError{Err: fmt.Errorf("%s is not in the future", req.Until.Local().Format("15:04"))}
	}
	if req.Until.Sub(now) > MaxExtra {
		return time.Time{}, actions.InputError{Err: fmt.Errorf("an extra allowance lasts at most %s; for longer, "+
			"change the rule in config.yaml or demote it", durationWords(MaxExtra))}
	}
	return req.Until.UTC(), nil
}

func durationWords(d time.Duration) string {
	if d%time.Hour == 0 {
		return fmt.Sprintf("%d hours", int(d/time.Hour))
	}
	return d.String()
}

// extraRules resolves the boundaries a grant lifts: those named, each a
// local rule, or every local rule that pauses on plan headroom. The
// organisation's rules are its own to lift.
func extraRules(rules []domain.Rule, named []string) ([]string, error) {
	if len(named) == 0 {
		var out []string
		for _, r := range rules {
			if r.Source != domain.SourceOrg && pausesOnHeadroom(r) && (r.Action == domain.ActionContinue || r.Action == domain.ActionSpend) {
				out = append(out, r.Name)
			}
		}
		if len(out) == 0 {
			return nil, actions.InputError{Err: fmt.Errorf("no boundary pauses on plan headroom; name the one to lift with --rule " +
				"(`tokenops boundaries status` lists them)")}
		}
		return out, nil
	}
	var out []string
	for _, name := range named {
		name = strings.TrimSpace(name)
		i := slices.IndexFunc(rules, func(r domain.Rule) bool { return r.Name == name })
		switch {
		case i < 0:
			return nil, actions.InputError{Err: fmt.Errorf("no boundary is called %q; `tokenops boundaries status` lists them", name)}
		case rules[i].Source == domain.SourceOrg:
			return nil, actions.InputError{Err: fmt.Errorf("%s is your organisation's rule: only the team plane lifts it", name)}
		}
		if !slices.Contains(out, name) {
			out = append(out, name)
		}
	}
	return out, nil
}

// ActiveExtras is the extra allowances in force now, newest first, each
// with what the presence check found: signed, or (under presence)
// refused with the reason, in which case it lifts nothing.
func ActiveExtras(env Env) []ExtraView {
	now := env.now()
	active := env.Dir.ActiveExtras(now)
	if len(active) == 0 {
		return nil
	}
	enforced := env.approval().Enforced()
	var (
		trust    presence.Trust
		trustErr error
	)
	if enforced || slices.ContainsFunc(active, func(e boundaryledger.Extra) bool { return e.Grant != nil }) {
		trust, trustErr = env.presenceDir().Trust(now)
	}
	out := make([]ExtraView, 0, len(active))
	for _, e := range active {
		v := extraView(e)
		switch res := checkExtra(e, trust, trustErr, now); {
		case res.OK():
			v.Presence, v.PresenceKey = PresenceSigned, res.KeyID
		case enforced:
			v.Presence, v.Ignored = PresenceRefused, res.Reason.Phrase()
		}
		out = append(out, v)
	}
	return out
}

// EndExtras ends every extra allowance in force now, recording each end on
// its decision. Ending only tightens, so any surface may do it.
func EndExtras(env Env) ([]ExtraView, error) {
	now := env.now()
	ended, err := env.Dir.EndExtras(now)
	if err != nil {
		return nil, err
	}
	out := make([]ExtraView, 0, len(ended))
	for _, e := range ended {
		if err := env.Dir.Append(extraEnvelope(e, eventschema.DecisionStageExpired, now)); err != nil {
			return nil, err
		}
		out = append(out, extraView(e))
	}
	return out, nil
}

// extraEnvelope records a grant, or its early end, as a decision: who
// granted it, which boundaries it lifts, until when and why.
func extraEnvelope(e boundaryledger.Extra, stage eventschema.DecisionStage, at time.Time) *eventschema.Envelope {
	rules := strings.Join(e.Rules, ", ")
	keep := eventschema.ResourceOption{Reason: "keep " + rules + " binding"}
	lift := eventschema.ResourceOption{Reason: "lift " + rules + " until " + e.Until.Format(time.RFC3339)}
	rationale := fmt.Sprintf("extra allowance: %s granted %s lifting %s until %s", e.Granter, e.At.Format(time.RFC3339), rules,
		e.Until.Format(time.RFC3339))
	if e.Why != "" {
		rationale += "; why: " + e.Why
	}
	if e.Grant != nil {
		rationale += "; signed in person with presence key " + e.Grant.Key +
			" (Touch ID, a paired Apple Watch or the login password); if you did not approve that dialog, end it with `tokenops boundaries extra --end`"
	}
	selected := lift
	if stage == eventschema.DecisionStageExpired {
		selected = keep
		rationale += "; ended early at " + at.Format(time.RFC3339)
	}
	return &eventschema.Envelope{
		ID: uuid.NewString(), SchemaVersion: eventschema.SchemaVersion, Type: eventschema.EventTypeDecision,
		Timestamp: at, Source: "boundary",
		Correlation: eventschema.Correlation{Decision: e.ID},
		Payload: &eventschema.DecisionEvent{
			Kind: "boundary", Stage: stage,
			Alternatives: []eventschema.ResourceOption{keep, lift}, Selected: selected,
			Policy: explain.BoundaryPolicyPrefix + e.Rules[0], Authority: "operator", Confidence: 1,
			Rationale: rationale, Executable: true, Adapter: e.Method,
			Actor:    eventschema.DecisionActor{User: e.Granter},
			Approval: extraApproval(e, e.Method, rules),
		},
	}
}

// extraApproval is the approval a grant records: who granted it, how,
// until when, and, for a signed grant, the presence key that signed it.
func extraApproval(e boundaryledger.Extra, method, rules string) eventschema.DecisionApproval {
	a := eventschema.DecisionApproval{
		Approver: e.Granter, Method: method, At: e.At, Rule: rules, Scope: "until " + e.Until.Format(time.RFC3339),
	}
	if e.Grant != nil {
		a.Presence, a.PresenceKey = PresenceSigned, e.Grant.Key
	}
	return a
}

// liftedBy is the extra allowance that lifts rule at now, if any.
func liftedBy(extras []boundaryledger.Extra, rule string, now time.Time) (boundaryledger.Extra, bool) {
	for _, e := range extras {
		if e.Covers(rule, now) {
			return e, true
		}
	}
	return boundaryledger.Extra{}, false
}

// liftedMatch is an enforced match an extra allowance let through.
type liftedMatch struct {
	match domain.Match
	extra boundaryledger.Extra
}

// liftExtras takes the rules an extra allowance lifts out of the binding
// decision: the strictest enforced match among the rest binds instead. The
// extras are read only when an enforced rule matched.
func liftExtras(v *domain.Verdict, relevant []domain.Rule, action domain.Action, facts domain.Facts, env Env) []liftedMatch {
	if v.Binding == nil {
		return nil
	}
	now := env.now()
	extras := honouredExtras(env, now)
	if len(extras) == 0 {
		return nil
	}
	var lifted []liftedMatch
	for _, m := range v.Matches {
		if !m.Rule.Enforced() {
			continue
		}
		if e, ok := liftedBy(extras, m.Rule.Name, now); ok {
			lifted = append(lifted, liftedMatch{match: m, extra: e})
		}
	}
	if len(lifted) == 0 {
		return nil
	}
	rest := slices.DeleteFunc(slices.Clone(relevant), func(r domain.Rule) bool {
		_, ok := liftedBy(extras, r.Name, now)
		return ok
	})
	v.Binding = domain.Evaluate(rest, action, facts).Binding
	return lifted
}

// recordLifted records, once per session, that an extra allowance let a
// call through a boundary that would have asked or refused, with who
// granted it and until when.
func (g *guard) recordLifted(l liftedMatch) error {
	id := domain.DecisionID(l.match.Rule.Name, g.call.SessionID)
	stage := eventschema.DecisionStageApplied
	if g.session.Recorded[id] == string(stage)+"+"+l.extra.ID {
		return nil
	}
	g.session.Recorded[id] = string(stage) + "+" + l.extra.ID
	g.changed = true
	approval := extraApproval(l.extra, MethodExtra, l.match.Rule.Name)
	env := decisionEnvelope(l.match, stage, g.call, g.env, g.now, g.evidence, approval)
	if d, ok := env.Payload.(*eventschema.DecisionEvent); ok {
		d.Rationale += fmt.Sprintf("; let through by the extra allowance %s, granted by %s until %s",
			l.extra.ID, l.extra.Granter, l.extra.Until.Format(time.RFC3339))
		if l.extra.Why != "" {
			d.Rationale += " (" + l.extra.Why + ")"
		}
		if l.extra.Grant != nil {
			d.Rationale += "; the grant is signed with presence key " + l.extra.Grant.Key
		}
	}
	return g.env.Dir.Append(env)
}
