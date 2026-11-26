package cursor

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Errors SessionCookie returns. None of them carries the token.
var (
	// ErrNotJWT is a sign-in that is not a JWT Cursor's client could read.
	ErrNotJWT = errors.New("cursor: the sign-in is not a readable access token")
	// ErrNoUserID is a sign-in whose subject names no user the cookie can carry.
	ErrNoUserID = errors.New("cursor: the sign-in names no user")
	// ErrNoExpiry is a sign-in that states no expiry, so it cannot be told apart from a stale one.
	ErrNoExpiry = errors.New("cursor: the sign-in states no expiry")
	// ErrSignInExpired is a sign-in Cursor no longer accepts: it has expired.
	ErrSignInExpired = errors.New("cursor: the sign-in has expired; sign in to Cursor again")
	// ErrEncrypted is a sign-in Cursor keeps encrypted at rest (Cursor 3.27 and
	// later, keyed by the "Cursor Safe Storage" Keychain item). TokenOps does not
	// decrypt it, because that would read a second Keychain item.
	ErrEncrypted = errors.New("cursor: Cursor keeps this sign-in encrypted, which TokenOps does not read; connect with the cookie instead")
)

// encryptedPrefix marks a sign-in Cursor encrypted (CodexBar #4790).
const encryptedPrefix = "cursor-safe-storage:"

// expiryMargin is how long a sign-in must still have to be used: one
// expiring sooner is refused, so a poll does not start on a dead token.
const expiryMargin = time.Minute

// SessionCookie returns the WorkosCursorSessionToken cookie value Cursor's
// own client sends for an app sign-in: "<user id>%3A%3A<access token>". The
// user ID is the subject's last "|" part (auth0|user_… is user_…), read from
// the token's claims without a signature check: the token is only sent to
// cursor.com, as Cursor's client does. Nothing is refreshed here.
func SessionCookie(token string, now time.Time) (string, error) {
	token = strings.TrimSpace(token)
	if strings.HasPrefix(token, encryptedPrefix) {
		return "", ErrEncrypted
	}
	if !isTokenText(token) {
		return "", ErrNotJWT
	}
	claims, err := tokenPayload(token)
	if err != nil {
		return "", err
	}
	userID, err := userIDOf(claims.Sub)
	if err != nil {
		return "", err
	}
	if claims.Exp == 0 {
		return "", ErrNoExpiry
	}
	if !time.Unix(claims.Exp, 0).After(now.Add(expiryMargin)) {
		return "", ErrSignInExpired
	}
	return userID + "%3A%3A" + token, nil
}

// tokenClaims is the payload of a JWT, unverified.
type tokenClaims struct {
	Sub string `json:"sub"`
	Exp int64  `json:"exp"`
}

func tokenPayload(token string) (tokenClaims, error) {
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return tokenClaims{}, ErrNotJWT
	}
	// Cursor's tokens use the URL-safe alphabet with or without padding; the
	// standard alphabet is accepted too, as CodexBar accepts it.
	payload := strings.TrimRight(strings.NewReplacer("-", "+", "_", "/").Replace(parts[1]), "=")
	raw, err := base64.RawStdEncoding.DecodeString(payload)
	if err != nil {
		return tokenClaims{}, ErrNotJWT
	}
	var c tokenClaims
	if err := json.Unmarshal(raw, &c); err != nil {
		return tokenClaims{}, fmt.Errorf("%w: its claims are not JSON", ErrNotJWT)
	}
	return c, nil
}

// userIDOf is the part of a subject Cursor's cookie carries: the text after
// the last "|", which must be a plain identifier.
func userIDOf(sub string) (string, error) {
	id := sub
	if i := strings.LastIndex(sub, "|"); i >= 0 {
		id = sub[i+1:]
	}
	if id == "" || !isIdentifier(id) {
		return "", ErrNoUserID
	}
	return id, nil
}

// isIdentifier holds a user ID to letters, digits and ". _ -", the only
// characters a cookie value carries unescaped.
func isIdentifier(s string) bool {
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '_', r == '-':
		default:
			return false
		}
	}
	return true
}

// isTokenText holds a token to the characters of a JWT, so nothing that
// ends a cookie value can ride in it.
func isTokenText(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '_', r == '-', r == '+', r == '/', r == '=':
		default:
			return false
		}
	}
	return true
}
