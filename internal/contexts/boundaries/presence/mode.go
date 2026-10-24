package presence

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

// Mode is how approvals are checked on this machine (ADR 0015 §5), the
// boundary_approval setting.
type Mode string

const (
	// ModeChannel trusts an approval by the channel it arrives on (the
	// operator's terminal, the harness's prompt). With a key enrolled the
	// presence check runs in shadow: it is recorded and refuses nothing.
	ModeChannel Mode = "channel"
	// ModePresence honours an approval only when it is signed by the
	// enrolled presence key, or attested by the harness's own permission
	// prompt.
	ModePresence Mode = "presence"
	// ModeStrict is ModePresence without the harness's prompt: only a
	// signed approval counts.
	ModeStrict Mode = "presence-strict"
)

// ParseMode reads a boundary_approval value; empty is ModeChannel.
func ParseMode(s string) (Mode, error) {
	switch m := Mode(strings.ToLower(strings.TrimSpace(s))); m {
	case "", ModeChannel:
		return ModeChannel, nil
	case ModePresence, ModeStrict:
		return m, nil
	default:
		return "", fmt.Errorf("boundary_approval must be %q, %q or %q, got %q", ModeChannel, ModePresence, ModeStrict, s)
	}
}

// Enforced reports whether approvals must be proven: presence or
// presence-strict.
func (m Mode) Enforced() bool { return m == ModePresence || m == ModeStrict }

// Strict reports whether the harness's prompt no longer approves.
func (m Mode) Strict() bool { return m == ModeStrict }

// EnrolmentDomain opens the statement a new key signs when it is enrolled,
// so enrolling raises the dialog once and proves the key can sign.
const EnrolmentDomain = "tokenops.presence-enrolment/v1\n"

// EnrolmentMessage is the exact bytes a new key signs at enrolment: its
// own id, the machine and user it was enrolled for, and when.
func EnrolmentMessage(key, machine, user string, at time.Time) []byte {
	var b strings.Builder
	b.WriteString(EnrolmentDomain)
	writeField(&b, "key", key)
	writeField(&b, "machine", machine)
	writeField(&b, "user", user)
	writeField(&b, "at", at.UTC().Truncate(time.Second).Format(time.RFC3339))
	return []byte(b.String())
}

// VerifyEnrolment checks a new key's signature over its enrolment
// statement.
func VerifyEnrolment(k Key, machine, user string, at time.Time, sig []byte) bool {
	return k.verify(EnrolmentMessage(k.ID, machine, user, at), sig)
}

// VerifyRotation checks the old key's signature over a rotation to a new
// key, the statement Reconcile accepts without a hold.
func VerifyRotation(old Key, r Rotation) bool {
	return old.verify(RotationMessage(r.From, r.To, r.At), r.Sig)
}

// RoutingRule is the rule a routing upgrade approval names. A routing
// proposal is not a boundary decision, so the approval statement binds the
// route's key as its decision and this name as its rule; the domain
// separation from a boundary approval is the decision id itself (bnd-… for
// a boundary, provider|from|to for a route).
const RoutingRule = "routing-upgrade"

// RoutingChallenge is a routing proposal's challenge: a digest of its key
// and when it was first raised. A route is decided once, so the raising
// it names is the only one; unlike a boundary's nonce it needs no store.
func RoutingChallenge(key string, raisedAt time.Time) string {
	sum := sha256.Sum256([]byte("tokenops.routing-challenge/v1\n" + key + "\n" + raisedAt.UTC().Format(time.RFC3339Nano)))
	return hex.EncodeToString(sum[:16])
}

// RoutingExpect is what a routing upgrade approval must be about. Its scope
// is session (a routing answer lasts), its crossings none: the key already
// names the move from one model to the other.
func RoutingExpect(key string, raisedAt, now time.Time) Expect {
	return Expect{
		Decision: key, Rule: RoutingRule, Scope: ScopeSession,
		Challenge: RoutingChallenge(key, raisedAt), Now: now,
	}
}
