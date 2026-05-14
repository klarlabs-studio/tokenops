// Package dashauth gates the dashboard's HTTP surface. The MVP supports
// two equivalent credential flows:
//
//   - Bearer token. Set TOKENOPS_ADMIN_TOKEN to a long random value;
//     clients send "Authorization: Bearer <token>" on every request.
//     Best for headless / scripted clients (CLI, MCP, CI).
//
//   - Password login + session cookie. Set TOKENOPS_ADMIN_PASSWORD;
//     POST it to /dashboard/login and the daemon returns a session
//     cookie clients reuse for the configured TTL. Best for the Vue
//     dashboard's interactive flow.
//
// OIDC is intentionally not implemented in this MVP; the OIDC type
// below stubs the interface so a future pluggable auth backend can
// wire in without changing the middleware contract.
package dashauth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Config tunes the auth surface.
type Config struct {
	// AdminPassword authenticates POST /dashboard/login. When empty,
	// password login is disabled; only token auth is available.
	AdminPassword string
	// AdminToken authorises requests that ship Authorization: Bearer.
	// When empty, token auth is disabled.
	AdminToken string
	// SessionTTL is how long an issued session cookie stays valid.
	// Default 24h.
	SessionTTL time.Duration
	// CookieName overrides the session cookie name. Default "tokenops_session".
	CookieName string
	// CookieSecure marks the cookie Secure. Default true in production
	// (TLS-enabled daemons); set false in dev so the cookie reaches a
	// plain-HTTP dashboard.
	CookieSecure bool
}

// Authenticator owns the in-memory session store + credential checks.
type Authenticator struct {
	cfg Config

	mu       sync.Mutex
	sessions map[string]time.Time
}

// New constructs an Authenticator from cfg. Returns an error when both
// credential flows are disabled — that would leave the dashboard fully
// open, which is never the operator's intent.
func New(cfg Config) (*Authenticator, error) {
	if cfg.AdminPassword == "" && cfg.AdminToken == "" {
		return nil, errors.New("dashauth: at least one of AdminPassword or AdminToken must be set")
	}
	if cfg.SessionTTL <= 0 {
		cfg.SessionTTL = 24 * time.Hour
	}
	if cfg.CookieName == "" {
		cfg.CookieName = "tokenops_session"
	}
	return &Authenticator{
		cfg:      cfg,
		sessions: make(map[string]time.Time),
	}, nil
}

// Middleware wraps next so unauthenticated requests are rejected with
// 401. Authentication accepts a bearer token header, a ?token=…
// query param, or a session cookie (all in constant time). When a
// query-param token authenticates a browser-style request (Accept
// header contains text/html), the middleware mints a session cookie
// and 303s back to the same URL without the token — so the dashboard
// reloads from cache without the secret in history.
func (a *Authenticator) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !a.authorize(r) {
			w.Header().Set("WWW-Authenticate", `Bearer realm="tokenops"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		// Browser-style bootstrap: query-param auth succeeded AND the
		// client looks like a browser. Mint a session cookie + redirect
		// so the URL bar drops the token.
		if q := r.URL.Query().Get("token"); q != "" && strings.Contains(r.Header.Get("Accept"), "text/html") {
			sid := a.mintSession()
			http.SetCookie(w, &http.Cookie{
				Name:     a.cfg.CookieName,
				Value:    sid,
				Path:     "/",
				HttpOnly: true,
				Secure:   a.cfg.CookieSecure,
				SameSite: http.SameSiteLaxMode,
				Expires:  time.Now().Add(a.cfg.SessionTTL),
			})
			clean := *r.URL
			values := clean.Query()
			values.Del("token")
			clean.RawQuery = values.Encode()
			http.Redirect(w, r, clean.RequestURI(), http.StatusSeeOther)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// mintSession allocates a random session ID and records it with the
// configured TTL. Exported separately from the password login flow so
// the query-param bootstrap reuses the same session map.
func (a *Authenticator) mintSession() string {
	buf := make([]byte, 32)
	_, _ = rand.Read(buf)
	sid := base64.RawURLEncoding.EncodeToString(buf)
	a.mu.Lock()
	a.sessions[sid] = time.Now().Add(a.cfg.SessionTTL)
	a.mu.Unlock()
	return sid
}

// authorize reports whether r presents a valid credential. Three
// credential channels are accepted, in order:
//
//   - Authorization: Bearer <token> — scripted / CLI clients.
//   - ?token=… query param — bootstrap path for the dashboard, where
//     the MCP tokenops_dashboard tool returns a clickable URL with
//     the token embedded. On a successful match we mint a short-lived
//     session cookie so the page reloads without leaking the token
//     in the address bar.
//   - Session cookie — established by the login handler or by the
//     query-param bootstrap above.
//
// All token comparisons use constant-time equality.
func (a *Authenticator) authorize(r *http.Request) bool {
	if a.cfg.AdminToken != "" {
		auth := r.Header.Get("Authorization")
		if strings.HasPrefix(auth, "Bearer ") {
			token := strings.TrimPrefix(auth, "Bearer ")
			if subtle.ConstantTimeCompare([]byte(token), []byte(a.cfg.AdminToken)) == 1 {
				return true
			}
		}
		if q := r.URL.Query().Get("token"); q != "" {
			if subtle.ConstantTimeCompare([]byte(q), []byte(a.cfg.AdminToken)) == 1 {
				return true
			}
		}
	}
	cookie, err := r.Cookie(a.cfg.CookieName)
	if err != nil || cookie.Value == "" {
		return false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	exp, ok := a.sessions[cookie.Value]
	if !ok {
		return false
	}
	if time.Now().After(exp) {
		delete(a.sessions, cookie.Value)
		return false
	}
	return true
}

// LoginHandler returns the http.Handler for POST /dashboard/login.
// The body is a JSON object {"password":"..."}; success mints a
// session cookie and returns the same value as JSON for clients that
// prefer header-based auth.
func (a *Authenticator) LoginHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if a.cfg.AdminPassword == "" {
			http.Error(w, "password login disabled", http.StatusForbidden)
			return
		}
		var body struct {
			Password string `json:"password"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); err != nil {
			http.Error(w, "invalid body", http.StatusBadRequest)
			return
		}
		if subtle.ConstantTimeCompare([]byte(body.Password), []byte(a.cfg.AdminPassword)) != 1 {
			http.Error(w, "invalid credentials", http.StatusUnauthorized)
			return
		}
		token, err := newSessionToken()
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		expires := time.Now().Add(a.cfg.SessionTTL)
		a.mu.Lock()
		a.sessions[token] = expires
		a.mu.Unlock()

		http.SetCookie(w, &http.Cookie{
			Name:     a.cfg.CookieName,
			Value:    token,
			Path:     "/",
			Expires:  expires,
			HttpOnly: true,
			Secure:   a.cfg.CookieSecure,
			SameSite: http.SameSiteLaxMode,
		})
		writeJSON(w, http.StatusOK, map[string]any{
			"session_token": token,
			"expires_at":    expires.UTC(),
		})
	})
}

// LogoutHandler returns the http.Handler for POST /dashboard/logout.
// Invalidates the presented session (cookie or bearer-style token).
func (a *Authenticator) LogoutHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if cookie, err := r.Cookie(a.cfg.CookieName); err == nil && cookie.Value != "" {
			a.mu.Lock()
			delete(a.sessions, cookie.Value)
			a.mu.Unlock()
			http.SetCookie(w, &http.Cookie{
				Name:    a.cfg.CookieName,
				Value:   "",
				Path:    "/",
				Expires: time.Unix(0, 0),
			})
		}
		w.WriteHeader(http.StatusNoContent)
	})
}

// SessionCount returns the number of currently-resident sessions.
// Useful for /readyz-style introspection.
func (a *Authenticator) SessionCount() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.sessions)
}

// Reap drops any session past its expiry. Designed to be called on a
// timer (60s) — the daemon does this from a background goroutine.
func (a *Authenticator) Reap() int {
	now := time.Now()
	a.mu.Lock()
	defer a.mu.Unlock()
	dropped := 0
	for k, v := range a.sessions {
		if now.After(v) {
			delete(a.sessions, k)
			dropped++
		}
	}
	return dropped
}

// newSessionToken returns a cryptographically random URL-safe token.
func newSessionToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// OIDCBackend is a stub interface for a future pluggable auth backend.
// The MVP does not implement it; daemons that need OIDC can drop in
// an implementation and wire it via Authenticator.WithOIDC (also a
// future addition). Documented here so the contract is visible at
// review time, not after the design has rotted.
type OIDCBackend interface {
	// Validate inspects an incoming request and returns the
	// authenticated subject (e.g. user email) or an error when the
	// credential is missing/invalid.
	Validate(r *http.Request) (subject string, err error)
}
