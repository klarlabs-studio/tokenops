// Package dashauth protects the daemon's local HTTP API. The package path
// and configuration key retain their historical names for compatibility.
package dashauth

import (
	"crypto/subtle"
	"errors"
	"net/http"
	"strings"
)

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

// Middleware rejects requests without a valid Authorization bearer token.
func (a *Authenticator) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		valid := strings.HasPrefix(auth, "Bearer ") &&
			subtle.ConstantTimeCompare([]byte(strings.TrimPrefix(auth, "Bearer ")), []byte(a.token)) == 1
		if !valid {
			w.Header().Set("WWW-Authenticate", `Bearer realm="tokenops"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}
