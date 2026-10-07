// Package dashauth decides whether a caller of the daemon's local HTTP API
// presented the API token. The HTTP middleware that applies the decision
// lives in internal/proxy; this package does no I/O. The package path and
// configuration key retain their historical names for compatibility.
package dashauth

import (
	"crypto/subtle"
	"errors"
	"strings"
)

// bearerPrefix is the Authorization scheme the API accepts.
const bearerPrefix = "Bearer "

// Config contains the shared secret used by local API clients.
type Config struct {
	AdminToken string
}

// Authenticator verifies bearer credentials for protected API routes.
type Authenticator struct {
	token string
}

// New constructs an Authenticator. An empty token would leave protected
// routes open, so callers must provide one explicitly.
func New(cfg Config) (*Authenticator, error) {
	if cfg.AdminToken == "" {
		return nil, errors.New("dashauth: AdminToken must be set")
	}
	return &Authenticator{token: cfg.AdminToken}, nil
}

// AuthorizeHeader reports whether authorization — the value of an
// Authorization header — carries the API token as a bearer credential.
// The comparison is constant-time. Only the header counts: a token
// passed any other way (a query parameter, a cookie) never authenticates.
func (a *Authenticator) AuthorizeHeader(authorization string) bool {
	if !strings.HasPrefix(authorization, bearerPrefix) {
		return false
	}
	presented := strings.TrimPrefix(authorization, bearerPrefix)
	return subtle.ConstantTimeCompare([]byte(presented), []byte(a.token)) == 1
}
