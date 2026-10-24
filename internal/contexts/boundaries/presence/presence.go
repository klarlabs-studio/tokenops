// Package presence is the presence check on boundary approvals (ADR 0015):
// an approval carries a statement about one decision, signed by a P-256 key
// only a person present at the machine can use, and the verifier checks the
// signature, every bound field and the statement's age.
//
// It is pure: it neither reads the key file nor raises a dialog. The key
// store (internal/infra/presence) reads the key and its pin; the signer is a
// helper outside the Go binaries. The Secure Enclave signs with
// CryptoKit's signature(for:), an ECDSA signature in DER over SHA-256 of
// the message; the public key is the raw X9.63 uncompressed point.
package presence

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/boundaries"
)

// Version is the signed statement's format. It is also in ApprovalDomain,
// so a statement of one version can never verify as another.
const Version = 1

// Domain separators: the first bytes of every message the presence key
// signs. Each kind of statement has its own, so a signature over one can
// never be presented as another (an approval as a rotation, or as the
// enrolment statement phase 4 signs).
const (
	ApprovalDomain  = "tokenops.approval/v1\n"
	RotationDomain  = "tokenops.presence-rotation/v1\n"
	crossingsDomain = "tokenops.crossings/v1\n"
)

// Scopes an approval can have; they match the answer's.
const (
	ScopeOnce    = "once"
	ScopeSession = "session"
)

// How long a signed approval is honoured after it was issued (ADR 0015
// §2): a one-step approval for ten minutes, a session approval for the
// answer's own 24 hours.
const (
	OnceTTL    = 10 * time.Minute
	SessionTTL = 24 * time.Hour
)

// ReplacementHold is how long approvals are refused after the key changed
// without the old key's signature (ADR 0015 §6).
const ReplacementHold = 15 * time.Minute

// clockSkew is how far in the future a statement may say it was issued:
// the signer and the verifier read the same clock, so only rounding.
const clockSkew = time.Minute

// ErrMalformed is a statement that cannot be encoded canonically.
var ErrMalformed = errors.New("malformed presence statement")

// Proof is a signed approval: the statement's fields and the signature
// over their canonical encoding (Message).
type Proof struct {
	V         int    `json:"v"`
	Key       string `json:"key"`
	Decision  string `json:"decision"`
	Rule      string `json:"rule"`
	Session   string `json:"session"`
	Scope     string `json:"scope"`
	Challenge string `json:"challenge"`
	// Crossed is CrossingsDigest of the crossings the person was shown.
	Crossed string `json:"crossed"`
	// Issued is when it was signed, in whole seconds, UTC.
	Issued time.Time `json:"issued"`
	// Sig is the ECDSA P-256 signature, ASN.1 DER, over SHA-256 of
	// Message().
	Sig []byte `json:"sig"`
}

// Message is the exact bytes the presence key signs: ApprovalDomain, then
// each field on its own line in a fixed order as name=<byte length>:value.
//
//	tokenops.approval/v1
//	key=19:pk-0123456789abcdef
//	decision=16:bnd-0123456789ab
//	rule=13:session-spend
//	session=2:s1
//	scope=4:once
//	challenge=32:…
//	crossed=64:…
//	issued=20:2026-10-24T12:00:00Z
//
// The length prefix makes the encoding unambiguous whatever a value holds
// (a session id is the harness's, and could hold a newline or "="), with
// no escaping or canonical-JSON rules for a signer in another language to
// reproduce. The version lives in the domain prefix.
func (p Proof) Message() ([]byte, error) {
	if p.V != Version {
		return nil, fmt.Errorf("%w: version %d", ErrMalformed, p.V)
	}
	if p.Scope != ScopeOnce && p.Scope != ScopeSession {
		return nil, fmt.Errorf("%w: scope %q", ErrMalformed, p.Scope)
	}
	if p.Key == "" || p.Decision == "" || p.Rule == "" || p.Challenge == "" || p.Crossed == "" {
		return nil, fmt.Errorf("%w: a bound field is empty", ErrMalformed)
	}
	if _, offset := p.Issued.Zone(); p.Issued.IsZero() || p.Issued.Nanosecond() != 0 || offset != 0 {
		return nil, fmt.Errorf("%w: issued must be whole seconds in UTC", ErrMalformed)
	}
	var b strings.Builder
	b.WriteString(ApprovalDomain)
	for _, f := range [][2]string{
		{"key", p.Key}, {"decision", p.Decision}, {"rule", p.Rule}, {"session", p.Session},
		{"scope", p.Scope}, {"challenge", p.Challenge}, {"crossed", p.Crossed},
		{"issued", p.Issued.UTC().Format(time.RFC3339)},
	} {
		writeField(&b, f[0], f[1])
	}
	return []byte(b.String()), nil
}

// Fingerprint names a statement, for spotting one presented twice. It
// hashes the statement, not the signature: an ECDSA signature can be
// altered into a second valid one over the same message, so a fingerprint
// of the signature could be dodged.
func (p Proof) Fingerprint() string {
	msg, err := p.Message()
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(msg)
	return hex.EncodeToString(sum[:16])
}

func writeField(b *strings.Builder, name, value string) {
	b.WriteString(name)
	b.WriteByte('=')
	b.WriteString(strconv.Itoa(len(value)))
	b.WriteByte(':')
	b.WriteString(value)
	b.WriteByte('\n')
}

// CrossingsDigest is the hash a proof binds of the crossings the person
// was shown, in the order shown: SHA-256, in hex, of the crossings domain
// prefix, n=<len>:<count>, then for each crossing the fields field, op,
// value, threshold, window and destination, encoded as Message encodes
// its fields. Figures are written in the shortest form that round-trips
// (strconv 'g', -1), so the same figures always hash the same.
func CrossingsDigest(cs []boundaries.Crossing) string {
	var b strings.Builder
	b.WriteString(crossingsDomain)
	writeField(&b, "n", strconv.Itoa(len(cs)))
	for _, c := range cs {
		writeField(&b, "field", string(c.Field))
		writeField(&b, "op", string(c.Op))
		writeField(&b, "value", strconv.FormatFloat(c.Value, 'g', -1, 64))
		writeField(&b, "threshold", strconv.FormatFloat(c.Threshold, 'g', -1, 64))
		writeField(&b, "window", string(c.Window))
		writeField(&b, "destination", c.Destination)
	}
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:])
}

// Key is a presence public key.
type Key struct {
	ID     string
	Public *ecdsa.PublicKey
	// Raw is the X9.63 uncompressed point (65 bytes) the key was read
	// from.
	Raw []byte
}

// ParseKey reads a raw X9.63 uncompressed P-256 public key, the form the
// helper stores. A point that is not on the curve is refused.
func ParseKey(raw []byte) (Key, error) {
	pub, err := ecdsa.ParseUncompressedPublicKey(elliptic.P256(), raw)
	if err != nil {
		return Key{}, fmt.Errorf("presence key: %w", err)
	}
	return Key{ID: KeyID(raw), Public: pub, Raw: append([]byte(nil), raw...)}, nil
}

// KeyID names a key by its public point: "pk-" and the first 8 bytes of
// its SHA-256, in hex.
func KeyID(raw []byte) string {
	sum := sha256.Sum256(raw)
	return "pk-" + hex.EncodeToString(sum[:8])
}

// verify checks sig over msg with k.
func (k Key) verify(msg, sig []byte) bool {
	if k.Public == nil || len(sig) == 0 {
		return false
	}
	sum := sha256.Sum256(msg)
	return ecdsa.VerifyASN1(k.Public, sum[:], sig)
}
