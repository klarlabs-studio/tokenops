package presence

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
)

// GrantDomain opens the statement an extra allowance's grant signs, so a
// signed grant can never be presented as an approval, a rotation or an
// enrolment, nor any of those as a grant.
const GrantDomain = "tokenops.extra-allowance/v1\n"

// MaxGrant is the longest span a signed grant may cover: the same day a
// session approval is honoured for.
const MaxGrant = 24 * time.Hour

// Reasons only a grant gives.
const (
	// ReasonGrantAltered: the grant's end time, start, granter or reason
	// are not the ones signed.
	ReasonGrantAltered Reason = "altered_grant"
)

// Grant is a signed extra allowance (ADR 0015, amendment "Extra
// allowances"): the operator lifting the named boundaries until a time,
// with who granted it and why, signed by the enrolled presence key.
type Grant struct {
	V   int    `json:"v"`
	Key string `json:"key"`
	// ID is the grant's decision id (bnd-…), unique per grant: it is the
	// statement's nonce, so one signature names exactly one grant.
	ID      string    `json:"id"`
	Rules   []string  `json:"rules"`
	Granter string    `json:"granter"`
	Why     string    `json:"why"`
	At      time.Time `json:"at"`
	Until   time.Time `json:"until"`
	// Sig is the ECDSA P-256 signature, ASN.1 DER, over SHA-256 of
	// Message().
	Sig []byte `json:"sig"`
}

// Message is the exact bytes the presence key signs for a grant:
// GrantDomain, then each field as name=<byte length>:value, the rules as
// their count followed by one rule= line each, in order.
//
//	tokenops.extra-allowance/v1
//	key=19:pk-0123456789abcdef
//	id=16:bnd-0123456789ab
//	rules=1:1
//	rule=14:headroom-pause
//	granter=5:felix
//	why=7:release
//	at=20:2026-10-24T12:00:00Z
//	until=20:2026-10-24T12:30:00Z
func (g Grant) Message() ([]byte, error) {
	switch {
	case g.V != Version:
		return nil, fmt.Errorf("%w: version %d", ErrMalformed, g.V)
	case g.Key == "" || g.ID == "" || g.Granter == "" || len(g.Rules) == 0 || slices.Contains(g.Rules, ""):
		return nil, fmt.Errorf("%w: a bound field is empty", ErrMalformed)
	case !wholeUTC(g.At) || !wholeUTC(g.Until):
		return nil, fmt.Errorf("%w: at and until must be whole seconds in UTC", ErrMalformed)
	case !g.Until.After(g.At) || g.Until.Sub(g.At) > MaxGrant:
		return nil, fmt.Errorf("%w: a grant ends after it starts and lasts at most %s", ErrMalformed, MaxGrant)
	}
	var b strings.Builder
	b.WriteString(GrantDomain)
	writeField(&b, "key", g.Key)
	writeField(&b, "id", g.ID)
	writeField(&b, "rules", strconv.Itoa(len(g.Rules)))
	for _, r := range g.Rules {
		writeField(&b, "rule", r)
	}
	writeField(&b, "granter", g.Granter)
	writeField(&b, "why", g.Why)
	writeField(&b, "at", g.At.Format(time.RFC3339))
	writeField(&b, "until", g.Until.Format(time.RFC3339))
	return []byte(b.String()), nil
}

func wholeUTC(t time.Time) bool {
	_, offset := t.Zone()
	return !t.IsZero() && t.Nanosecond() == 0 && offset == 0
}

// GrantExpect is the grant as the extras file records it: a signature
// lifts only what it was made over.
type GrantExpect struct {
	ID      string
	Rules   []string
	Granter string
	Why     string
	At      time.Time
	Until   time.Time
	Now     time.Time
}

// VerifyGrant checks a signed grant against the trusted key and the grant
// it is stored with. A nil grant is an unsigned one. Like Verify, the
// cheap field checks run first and the signature last; a grant past its
// end is expired.
func VerifyGrant(g *Grant, e GrantExpect, t Trust) Result {
	switch {
	case t.State == TrustNotEnrolled:
		return Result{Reason: ReasonNotEnrolled}
	case g == nil:
		return Result{Reason: ReasonUnsigned}
	}
	res := Result{KeyID: g.Key}
	msg, err := g.Message()
	switch {
	case err != nil:
		res.Reason = ReasonMalformed
	case t.State == TrustHeld:
		res.Reason = ReasonKeyHeld
	case g.Key != t.Key.ID:
		res.Reason = ReasonWrongKey
	case g.ID != e.ID:
		res.Reason = ReasonDecision
	case !slices.Equal(g.Rules, e.Rules):
		res.Reason = ReasonRule
	case g.Granter != e.Granter || g.Why != e.Why || !g.At.Equal(e.At) || !g.Until.Equal(e.Until):
		res.Reason = ReasonGrantAltered
	case !t.Key.verify(msg, g.Sig):
		res.Reason = ReasonSignature
	case g.At.Before(t.ValidFrom.Truncate(time.Second)):
		res.Reason = ReasonBeforeKey
	case g.At.After(e.Now.UTC().Add(clockSkew)):
		res.Reason = ReasonFuture
	case !e.Now.Before(g.Until):
		res.Reason = ReasonExpired
	}
	return res
}
