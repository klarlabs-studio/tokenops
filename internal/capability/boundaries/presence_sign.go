package boundaries

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"go.klarlabs.de/tokenops/internal/capability/actions"
	"go.klarlabs.de/tokenops/internal/config"
	domain "go.klarlabs.de/tokenops/internal/contexts/boundaries"
	"go.klarlabs.de/tokenops/internal/contexts/boundaries/presence"
	"go.klarlabs.de/tokenops/internal/infra/boundaryledger"
	infrapresence "go.klarlabs.de/tokenops/internal/infra/presence"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// Signer is the presence helper (ADR 0015 §1): it creates a key whose
// every signature needs a person present, and signs with it, raising the
// system dialog with reason as its text. infrapresence.Helper satisfies
// it; tests use a fake with a software key.
type Signer interface {
	Create(ctx context.Context, blobPath string) ([]byte, error)
	Sign(ctx context.Context, blobPath string, msg []byte, reason string) ([]byte, error)
}

// ErrPresenceUnavailable wraps every reason an approval cannot be signed
// on this machine (no key, a held key, no helper, nobody at the screen),
// as opposed to a person cancelling the dialog.
var ErrPresenceUnavailable = errors.New("presence approval unavailable")

type unavailable struct{ msg string }

func (u unavailable) Error() string { return u.msg }
func (u unavailable) Unwrap() error { return ErrPresenceUnavailable }

// ApprovalStatement is an approval of one waiting decision, ready for the
// presence key to sign: built here, never by a surface, so the bytes the
// person's dialog approves are exactly the ones the verifier checks
// (ADR 0015 §2). A surface that cannot link the helper's caller (the menu
// bar) asks the daemon for one, has the helper sign Statement with Reason
// as the dialog's text, and posts Proof back with sig set.
type ApprovalStatement struct {
	// Proof is the statement's fields, unsigned: post it back as
	// presence_proof with sig, the helper's signature, added.
	Proof presence.Proof `json:"proof"`
	// Statement is the canonical bytes the key signs (tokenops.approval/v1).
	Statement []byte `json:"statement"`
	// Reason is the dialog's text, already made safe to show: it names the
	// rule, the action and the session, because a paired Apple Watch shows
	// only this.
	Reason string `json:"reason"`
	// Blob is the enrolled key's blob, which the helper signs with.
	Blob string `json:"blob"`
	// Key is the enrolled key the statement is made under.
	Key string `json:"key"`
}

// PrepareApproval builds the statement approving the decision waiting on
// id: it names the decision, its rule and session, the scope, the
// raising's challenge and the crossings shown, issued now. It needs an
// enrolled, unheld key with its blob on this machine (an error wrapping
// ErrPresenceUnavailable otherwise) and a decision still waiting (an
// input error otherwise). A decision raised before challenges existed is
// given one, so the approval names this raising.
func PrepareApproval(env Env, id string, session bool) (ApprovalStatement, error) {
	id = strings.TrimSpace(id)
	if !domain.ValidDecisionID(id) {
		return ApprovalStatement{}, actions.InputError{Err: fmt.Errorf("%q is not a boundary decision id (bnd-…)", id)}
	}
	trust, err := signingTrust(env)
	if err != nil {
		return ApprovalStatement{}, err
	}
	p, ok, err := env.Dir.GetPending(id)
	if err != nil {
		return ApprovalStatement{}, err
	}
	if !ok {
		return ApprovalStatement{}, actions.InputError{Err: fmt.Errorf("no boundary decision %s is waiting on an answer", id)}
	}
	if teamRouted(p) {
		return ApprovalStatement{}, actions.InputError{Err: fmt.Errorf("%s is your organisation's rule: its approver answers in the team plane, "+
			"not this machine; you can still deny it here", p.Rule)}
	}
	if p.Challenge == "" {
		p.Challenge = newChallenge()
		if err := env.Dir.PutPending(p); err != nil {
			return ApprovalStatement{}, err
		}
	}
	scope := boundaryledger.ScopeOnce
	if session {
		scope = boundaryledger.ScopeSession
	}
	proof := presence.Proof{
		V: presence.Version, Key: trust.Key.ID, Decision: id, Rule: p.Rule, Session: p.SessionID,
		Scope: scope, Challenge: p.Challenge, Crossed: presence.CrossingsDigest(p.Crossings),
		Issued: env.now().UTC().Truncate(time.Second),
	}
	msg, err := proof.Message()
	if err != nil {
		return ApprovalStatement{}, err
	}
	return ApprovalStatement{
		Proof: proof, Statement: msg, Reason: infrapresence.DialogText(approvalDialog(p, scope)),
		Blob: env.presenceDir().BlobPath(), Key: trust.Key.ID,
	}, nil
}

// SignApproval asks the person at this Mac to sign an approval of the
// decision waiting on id (PrepareApproval's statement), raising the dialog
// with its reason. The proof is checked against the enrolled key before
// it is returned, so a blob that does not match the key is caught here,
// not at the pause point.
func SignApproval(ctx context.Context, env Env, s Signer, id string, session bool) (*presence.Proof, error) {
	st, err := PrepareApproval(env, id, session)
	if err != nil {
		return nil, err
	}
	proof := st.Proof
	if proof.Sig, err = s.Sign(ctx, st.Blob, st.Statement, st.Reason); err != nil {
		return nil, signError(err)
	}
	if res := verifyPrepared(env, &proof); !res.OK() {
		return nil, fmt.Errorf("the signature the presence helper returned is not valid (%s): re-enrol with `%s --replace`",
			res.Reason.Phrase(), EnrolCommand)
	}
	return &proof, nil
}

// verifyPrepared checks a signed approval against the decision it names
// as it waits now, with the scope the proof claims.
func verifyPrepared(env Env, proof *presence.Proof) presence.Result {
	p, ok, err := env.Dir.GetPending(proof.Decision)
	if err != nil || !ok {
		return presence.Result{Reason: presence.ReasonDecision}
	}
	return verifyAnswerProof(env, p, proof.Scope, proof)
}

// approvalDialog is the dialog's text: "<helper> is trying to approve …".
// It names the rule, the action and the session (shortened), what
// crossed, and the scope.
func approvalDialog(p boundaryledger.Pending, scope string) string {
	what := "the next step only"
	if scope == boundaryledger.ScopeSession {
		what = "the rest of the session"
	}
	return fmt.Sprintf("approve TokenOps boundary %q (%s) in %s session %s: %s, for %s",
		p.Rule, p.Action, p.Harness, shortID(p.SessionID), p.Crossed, what)
}

func shortID(s string) string {
	if r := []rune(s); len(r) > 8 {
		return string(r[:8])
	}
	return s
}

// signingTrust is the key a signature is made under: pinned, with its blob
// on this machine.
func signingTrust(env Env) (presence.Trust, error) {
	trust, err := env.presenceDir().Trust(env.now())
	if err != nil {
		return presence.Trust{}, unavailable{"the presence key store could not be read: " + failureClass(err)}
	}
	switch trust.State {
	case presence.TrustNotEnrolled:
		return presence.Trust{}, unavailable{"no presence key is enrolled on this machine: enrol one with `" + EnrolCommand + "`"}
	case presence.TrustHeld:
		return presence.Trust{}, unavailable{"approvals are held until " + trust.Pin.HoldUntil.Local().Format("15:04") +
			": the presence key was replaced without the old key's signature"}
	}
	if !env.presenceDir().HasBlob() {
		return presence.Trust{}, unavailable{"the presence key is enrolled but its key blob is not on this machine: " +
			"re-enrol with `" + EnrolCommand + " --replace`"}
	}
	return trust, nil
}

// signError names why no signature came back.
func signError(err error) error {
	if infrapresence.Unavailable(err) {
		return unavailable{err.Error()}
	}
	return err
}

// verifyAnswerProof checks the proof an approval arrives with against the
// decision as it waits.
func verifyAnswerProof(env Env, p boundaryledger.Pending, scope string, proof *presence.Proof) presence.Result {
	trust, err := env.presenceDir().Trust(env.now())
	if err != nil {
		return presence.Result{Reason: presence.ReasonKeyUnreadable}
	}
	return presence.Verify(proof, presence.Expect{
		Decision: p.ID, Rule: p.Rule, Session: p.SessionID, Scope: scope, Challenge: p.Challenge,
		Crossings: p.Crossings, Now: env.now(),
		Spent: func(fp string) bool { return slices.Contains(p.Spent, fp) },
	}, trust)
}

// Enrolled is what enrolment or rotation did.
type Enrolled struct {
	KeyID    string     `json:"key_id"`
	Previous string     `json:"previous,omitempty"`
	How      string     `json:"how"`
	Since    time.Time  `json:"since"`
	HoldTill *time.Time `json:"hold_until,omitempty"`
	Dir      string     `json:"dir"`
	Note     string     `json:"note"`
}

// EnrollOptions shape an enrolment.
type EnrollOptions struct {
	// Replace enrols a new key over an existing one without its signature,
	// which holds approvals for 15 minutes (ADR 0015 §6).
	Replace bool
	// Machine names this Mac in the enrolment statement.
	Machine string
}

// EnrollPresence creates this Mac's presence key, has it sign its
// enrolment statement (one dialog, which proves the key can sign), and
// installs it where the pause point reads it. A key already enrolled is
// kept unless opts.Replace; RotatePresence moves to a new key with the old
// key's signature instead.
func EnrollPresence(ctx context.Context, env Env, s Signer, opts EnrollOptions) (Enrolled, error) {
	dir := env.presenceDir()
	trust, err := dir.Trust(env.now())
	if err != nil {
		return Enrolled{}, err
	}
	if trust.State != presence.TrustNotEnrolled && !opts.Replace {
		return Enrolled{}, actions.InputError{Err: fmt.Errorf("presence key %s is already enrolled: "+
			"`tokenops boundaries presence rotate` moves to a new key with its signature (no hold); "+
			"`%s --replace` replaces it without one and holds approvals for 15 minutes", trust.Key.ID, EnrolCommand)}
	}
	pub, enrolment, err := createKey(ctx, env, s, opts.Machine, "enrol")
	if err != nil {
		return Enrolled{}, err
	}
	if err := dir.Install(pub, enrolment, nil); err != nil {
		dir.Discard()
		return Enrolled{}, err
	}
	return pinned(env, "enrolled")
}

// RotatePresence moves to a new presence key with the old key's
// signature: two dialogs, the old key signing the move and the new one its
// enrolment. A rotation the old key signed holds nothing.
func RotatePresence(ctx context.Context, env Env, s Signer, machine string) (Enrolled, error) {
	dir := env.presenceDir()
	old, err := signingTrust(env)
	if err != nil {
		return Enrolled{}, err
	}
	pub, enrolment, err := createKey(ctx, env, s, machine, "rotate to")
	if err != nil {
		return Enrolled{}, err
	}
	rot := presence.Rotation{From: old.Key.ID, To: enrolment.Key, At: enrolment.At}
	reason := fmt.Sprintf("rotate this Mac's TokenOps approval key from %s to %s", old.Key.ID, enrolment.Key)
	if rot.Sig, err = s.Sign(ctx, dir.BlobPath(), presence.RotationMessage(rot.From, rot.To, rot.At), reason); err != nil {
		dir.Discard()
		return Enrolled{}, signError(err)
	}
	if !presence.VerifyRotation(old.Key, rot) {
		dir.Discard()
		return Enrolled{}, fmt.Errorf("the old key's signature over the rotation is not valid: its blob does not match key %s", old.Key.ID)
	}
	if err := dir.Install(pub, enrolment, &rot); err != nil {
		dir.Discard()
		return Enrolled{}, err
	}
	return pinned(env, "rotated")
}

// createKey makes a key at the store's waiting path and has it sign its
// enrolment statement.
func createKey(ctx context.Context, env Env, s Signer, machine, verb string) ([]byte, infrapresence.Enrolment, error) {
	dir := env.presenceDir()
	if err := dir.Prepare(); err != nil {
		return nil, infrapresence.Enrolment{}, err
	}
	pub, err := s.Create(ctx, dir.NewBlobPath())
	if err != nil {
		dir.Discard()
		return nil, infrapresence.Enrolment{}, signError(err)
	}
	key, err := presence.ParseKey(pub)
	if err != nil {
		dir.Discard()
		return nil, infrapresence.Enrolment{}, fmt.Errorf("the presence helper returned no valid key: %w", err)
	}
	if machine == "" {
		machine, _ = os.Hostname()
	}
	e := infrapresence.Enrolment{Key: key.ID, Machine: machine, User: env.user(), At: env.now().Truncate(time.Second)}
	reason := fmt.Sprintf("%s TokenOps approval key %s for %s on %s", verb, key.ID, e.User, e.Machine)
	if e.Sig, err = s.Sign(ctx, dir.NewBlobPath(), presence.EnrolmentMessage(e.Key, e.Machine, e.User, e.At), reason); err != nil {
		dir.Discard()
		return nil, infrapresence.Enrolment{}, signError(err)
	}
	if !presence.VerifyEnrolment(key, e.Machine, e.User, e.At, e.Sig) {
		dir.Discard()
		return nil, infrapresence.Enrolment{}, errors.New("the new key's signature over its enrolment statement is not valid")
	}
	return pub, e, nil
}

// pinned reads the store back, pinning the new key, and records it.
func pinned(env Env, verb string) (Enrolled, error) {
	trust, err := env.presenceDir().Trust(env.now())
	if err != nil {
		return Enrolled{}, err
	}
	out := Enrolled{
		KeyID: trust.Key.ID, Previous: trust.Pin.Previous, How: trust.Pin.How, Since: trust.Pin.Since,
		Dir: string(env.presenceDir()),
	}
	switch trust.Pin.How {
	case presence.PinReplaced:
		hold := trust.Pin.HoldUntil
		out.HoldTill = &hold
		out.Note = fmt.Sprintf("presence key %s replaces %s without its signature: approvals are held until %s, "+
			"and the glance and `tokenops boundaries status` say so", out.KeyID, out.Previous, hold.Local().Format("15:04"))
	case presence.PinRotated:
		out.Note = fmt.Sprintf("rotated from %s to %s, signed by the old key; nothing is held", out.Previous, out.KeyID)
	default:
		out.Note = fmt.Sprintf("presence key %s %s", out.KeyID, verb)
	}
	if !env.approval().Enforced() {
		out.Note += "; approvals are signed and checked in shadow until boundary_approval: presence (`tokenops boundaries presence enforce`)"
	}
	return out, env.Dir.Append(presenceEnvelope(env, out))
}

// presenceEnvelope records a key change on the boundary ledger, where
// `tokenops explain presence:<key id>` finds it.
func presenceEnvelope(env Env, e Enrolled) *eventschema.Envelope {
	stage := "presence_" + e.How
	return &eventschema.Envelope{
		ID: uuid.NewString(), SchemaVersion: eventschema.SchemaVersion, Type: eventschema.EventTypeDecision,
		Timestamp: env.now(), Source: "boundary",
		Correlation: eventschema.Correlation{Decision: "presence:" + e.KeyID},
		Payload: &eventschema.DecisionEvent{
			Kind: "presence", Stage: eventschema.DecisionStageApplied,
			Rationale: stage + ": " + e.Note, Adapter: "cli",
			Actor: eventschema.DecisionActor{User: env.user()},
		},
	}
}

// ApprovalSet is what changing boundary_approval did.
type ApprovalSet struct {
	Approval string `json:"approval"`
	Config   string `json:"config"`
	Note     string `json:"note"`
}

// SetApproval writes boundary_approval to the config at path. Turning
// enforcement on with no key enrolled is refused: it would leave nothing
// that can approve.
func SetApproval(path string, env Env, mode presence.Mode) (ApprovalSet, error) {
	if mode.Enforced() {
		if st := Presence(env); st.State == PresenceStateNotEnrolled || st.State == PresenceStateUnavailable {
			return ApprovalSet{}, actions.InputError{Err: fmt.Errorf("no presence key is enrolled on this machine, "+
				"so nothing could approve with boundary_approval: %s; enrol one first with `%s`", mode, EnrolCommand)}
		}
	}
	cfg, err := config.ReadMutable(path)
	if err != nil {
		return ApprovalSet{}, actions.InputError{Err: err}
	}
	cfg.BoundaryApproval = string(mode)
	if mode == presence.ModeChannel {
		cfg.BoundaryApproval = ""
	}
	if err := cfg.Validate(); err != nil {
		return ApprovalSet{}, actions.InputError{Err: err}
	}
	if err := config.WriteMutable(path, cfg); err != nil {
		return ApprovalSet{}, err
	}
	out := ApprovalSet{Approval: string(mode), Config: path}
	switch mode {
	case presence.ModePresence:
		out.Note = "an approval now counts only when signed with this Mac's presence key, or given at Claude Code's prompt"
	case presence.ModeStrict:
		out.Note = "an approval now counts only when signed with this Mac's presence key; Claude Code's prompt no longer approves"
	default:
		out.Note = "approvals are trusted by the channel they arrive on again; the presence check runs in shadow"
	}
	out.Note += ". The pause point reads it at the next tool call; the daemon's router and uploads at its next restart (`tokenops daemon restart`)"
	return out, nil
}

// ApprovalMode is boundary_approval, and Proof a presence-signed approval,
// for adapters, which reach the domain only through this package.
type (
	ApprovalMode = presence.Mode
	Proof        = presence.Proof
)

// The boundary_approval values.
const (
	ApprovalChannel  = presence.ModeChannel
	ApprovalPresence = presence.ModePresence
	ApprovalStrict   = presence.ModeStrict
)
