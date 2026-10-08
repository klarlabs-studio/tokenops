package team

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"errors"
	"strings"
	"time"
)

// TokenKind says what a credential is for. The kind is part of the token's
// text, so a token presented to the wrong endpoint is refused before any
// lookup.
type TokenKind string

// The credentials the team plane issues. Each is 256 bits from crypto/rand,
// shown once, and stored only as its SHA-256: a leaked database holds no
// usable credential. A slow hash buys nothing for tokens with this much
// entropy.
const (
	// TokenDevice authenticates one enrolled machine's uploads.
	TokenDevice TokenKind = "dev"
	// TokenInvite enrols one machine, once, before it expires.
	TokenInvite TokenKind = "inv"
	// TokenLogin is a single-use link to the web view.
	TokenLogin TokenKind = "lnk"
	// TokenSession is a signed-in browser.
	TokenSession TokenKind = "ses"
	// TokenAdmin is an owner's or admin's API credential.
	TokenAdmin TokenKind = "adm"
)

// Lifetimes of the short-lived credentials.
const (
	InviteTTL  = 7 * 24 * time.Hour
	LoginTTL   = 10 * time.Minute
	SessionTTL = 12 * time.Hour
)

const tokenPrefix = "tot_"

var tokenEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)

// NewToken mints a credential of kind k and returns its text, to hand out
// once, and its hash, to store.
func NewToken(k TokenKind) (plain string, hash []byte, err error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", nil, err
	}
	plain = tokenPrefix + string(k) + "_" + strings.ToLower(tokenEncoding.EncodeToString(raw[:]))
	return plain, HashToken(plain), nil
}

// HashToken is what is stored for, and looked up by, a token.
func HashToken(plain string) []byte {
	sum := sha256.Sum256([]byte(plain))
	return sum[:]
}

// ErrMalformedToken is returned for text that is not a team-plane token.
var ErrMalformedToken = errors.New("not a TokenOps team token")

// KindOf reads the kind of a token without trusting anything else in it.
func KindOf(plain string) (TokenKind, error) {
	rest, ok := strings.CutPrefix(plain, tokenPrefix)
	if !ok || len(rest) < 5 || rest[3] != '_' {
		return "", ErrMalformedToken
	}
	k := TokenKind(rest[:3])
	switch k {
	case TokenDevice, TokenInvite, TokenLogin, TokenSession, TokenAdmin:
	default:
		return "", ErrMalformedToken
	}
	body := rest[4:]
	if len(body) != 52 {
		return "", ErrMalformedToken
	}
	for _, c := range body {
		if (c < 'a' || c > 'z') && (c < '2' || c > '7') {
			return "", ErrMalformedToken
		}
	}
	return k, nil
}
