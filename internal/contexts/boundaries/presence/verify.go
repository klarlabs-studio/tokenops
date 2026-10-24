package presence

import (
	"errors"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/boundaries"
)

// Reason is why the verifier would not honour an approval; empty when it
// would.
type Reason string

// Reasons, each a refusal.
const (
	ReasonNone        Reason = ""
	ReasonNotEnrolled Reason = "not_enrolled"
	ReasonUnsigned    Reason = "unsigned"
	ReasonMalformed   Reason = "malformed"
	ReasonKeyHeld     Reason = "key_replaced_hold"
	ReasonWrongKey    Reason = "wrong_key"
	ReasonDecision    Reason = "wrong_decision"
	ReasonRule        Reason = "wrong_rule"
	ReasonSession     Reason = "wrong_session"
	ReasonScope       Reason = "wrong_scope"
	ReasonChallenge   Reason = "wrong_challenge"
	ReasonCrossings   Reason = "altered_crossings"
	ReasonSignature   Reason = "bad_signature"
	ReasonBeforeKey   Reason = "issued_before_key"
	ReasonFuture      Reason = "issued_in_future"
	ReasonExpired     Reason = "expired"
	ReasonSpent       Reason = "already_used"
	// ReasonKeyUnreadable: the key store could not be read, so nothing
	// could be checked.
	ReasonKeyUnreadable Reason = "key_store_unreadable"
)

// Phrase is the reason in words, for a decision's rationale and status.
func (r Reason) Phrase() string {
	switch r {
	case ReasonNone:
		return "signed by the enrolled presence key"
	case ReasonNotEnrolled:
		return "no presence key is enrolled on this machine"
	case ReasonUnsigned:
		return "the answer carries no presence signature"
	case ReasonMalformed:
		return "the answer's presence statement is malformed"
	case ReasonKeyHeld:
		return "the presence key was replaced without the old key's signature and approvals are held"
	case ReasonWrongKey:
		return "it was signed by a key other than the enrolled one"
	case ReasonDecision:
		return "it was signed for another decision"
	case ReasonRule:
		return "it was signed for another rule"
	case ReasonSession:
		return "it was signed for another session"
	case ReasonScope:
		return "it was signed for another scope"
	case ReasonChallenge:
		return "it answers an earlier raising of this decision"
	case ReasonCrossings:
		return "the crossings it records are not the ones signed"
	case ReasonSignature:
		return "the signature does not verify"
	case ReasonBeforeKey:
		return "it was issued before the current key was trusted"
	case ReasonFuture:
		return "it says it was issued in the future"
	case ReasonExpired:
		return "it has expired"
	case ReasonSpent:
		return "this one-step approval was already used"
	case ReasonKeyUnreadable:
		return "the presence key store could not be read"
	}
	return string(r)
}

// Expect is what an approval must be about: the decision as it waits, and
// the answer that carries the proof.
type Expect struct {
	Decision string
	Rule     string
	Session  string
	// Scope is the answer's scope: a one-step proof cannot be stretched
	// into a session approval by editing the answer.
	Scope string
	// Challenge is the pending decision's current nonce.
	Challenge string
	// Crossings are the figures the answer records as shown.
	Crossings []boundaries.Crossing
	Now       time.Time
	// Spent reports whether a one-step statement with this fingerprint
	// was already consumed; nil when none was.
	Spent func(fingerprint string) bool
}

// Result is the verifier's answer.
type Result struct {
	Reason Reason
	KeyID  string
}

// OK reports whether the approval would be honoured.
func (r Result) OK() bool { return r.Reason == ReasonNone }

// Verify checks a proof against the trusted key and what it must be
// about. A nil proof is an unsigned answer. Checks run cheapest first, so
// the reason is the first that fails; the signature is checked over the
// statement's own canonical encoding once every field agrees with e.
func Verify(p *Proof, e Expect, t Trust) Result {
	switch {
	case t.State == TrustNotEnrolled:
		return Result{Reason: ReasonNotEnrolled}
	case p == nil:
		return Result{Reason: ReasonUnsigned}
	}
	res := Result{KeyID: p.Key}
	msg, err := p.Message()
	if r := firstMismatch(p, e, t, err); r != ReasonNone {
		res.Reason = r
		return res
	}
	if !t.Key.verify(msg, p.Sig) {
		res.Reason = ReasonSignature
		return res
	}
	res.Reason = timing(p, e, t)
	return res
}

func firstMismatch(p *Proof, e Expect, t Trust, msgErr error) Reason {
	switch {
	case errors.Is(msgErr, ErrMalformed):
		return ReasonMalformed
	case t.State == TrustHeld:
		return ReasonKeyHeld
	case p.Key != t.Key.ID:
		return ReasonWrongKey
	case p.Decision != e.Decision:
		return ReasonDecision
	case p.Rule != e.Rule:
		return ReasonRule
	case p.Session != e.Session:
		return ReasonSession
	case p.Scope != e.Scope:
		return ReasonScope
	case e.Challenge == "" || p.Challenge != e.Challenge:
		return ReasonChallenge
	case p.Crossed != CrossingsDigest(e.Crossings):
		return ReasonCrossings
	}
	return ReasonNone
}

// timing checks the proof's age against its scope and the key's pin, and
// that a one-step proof was not used before.
func timing(p *Proof, e Expect, t Trust) Reason {
	now := e.Now.UTC()
	ttl := OnceTTL
	if p.Scope == ScopeSession {
		ttl = SessionTTL
	}
	switch {
	case p.Issued.Before(t.ValidFrom.Truncate(time.Second)):
		return ReasonBeforeKey
	case p.Issued.After(now.Add(clockSkew)):
		return ReasonFuture
	case now.Sub(p.Issued) > ttl:
		return ReasonExpired
	case p.Scope == ScopeOnce && e.Spent != nil && e.Spent(p.Fingerprint()):
		return ReasonSpent
	}
	return ReasonNone
}
