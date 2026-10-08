// Package teamserver is the team plane's HTTP service (ADR 0012): the API
// joined machines upload derived figures to, the aggregate and drill-down
// reads, and a small server-rendered web view. It runs behind Caddy, which
// terminates TLS, and keeps its state in Postgres (pgstore).
package teamserver

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/team"
	"go.klarlabs.de/tokenops/internal/teamserver/pgstore"
	"go.klarlabs.de/tokenops/pkg/teamwire"
)

// Config configures the service.
type Config struct {
	// PublicURL is the address members reach the service at, e.g.
	// https://team.example.eu. Login links are built from it, and its
	// scheme decides whether the session cookie is marked Secure.
	PublicURL string
	// TrustedProxies are the peers whose X-Forwarded-For is believed when
	// rate limiting by client address: the reverse proxy.
	TrustedProxies []netip.Prefix
	// AuditRetentionDays is how long the audit log is kept.
	AuditRetentionDays int
	Logger             *slog.Logger
}

// Server serves the API and the web view.
type Server struct {
	store   *pgstore.Store
	cfg     Config
	log     *slog.Logger
	public  *url.URL
	byIP    *limiter
	byDev   *limiter
	pages   *pages
	started time.Time
}

// New builds the service.
func New(store *pgstore.Store, cfg Config) (*Server, error) {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	u, err := url.Parse(strings.TrimRight(cfg.PublicURL, "/"))
	if err != nil || u.Scheme == "" || u.Host == "" {
		return nil, errors.New("public URL: want an absolute URL such as https://team.example.eu")
	}
	p, err := loadPages()
	if err != nil {
		return nil, err
	}
	return &Server{
		store: store, cfg: cfg, log: cfg.Logger, public: u,
		// Enrolment and sign-in by address: a burst of 10, then one every
		// 6 seconds. Uploads by device: a burst of 6, then one a minute;
		// the daemon uploads hourly.
		byIP:    newLimiter(6*time.Second, 10),
		byDev:   newLimiter(time.Minute, 6),
		pages:   p,
		started: time.Now(),
	}, nil
}

// Handler is the service's routes behind its middleware.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok\n")) })
	mux.HandleFunc("GET /readyz", s.ready)

	mux.HandleFunc("POST /api/v1/enroll", s.limitIP(s.enroll))
	mux.HandleFunc("POST /api/v1/ingest", s.ingest)
	mux.HandleFunc("GET /api/v1/me", s.api(s.me))
	mux.HandleFunc("DELETE /api/v1/devices/self", s.leave)
	mux.HandleFunc("POST /api/v1/login-links", s.limitIP(s.api(s.loginLink)))
	mux.HandleFunc("GET /api/v1/aggregates", s.api(s.aggregates))
	mux.HandleFunc("GET /api/v1/members", s.api(s.members))
	mux.HandleFunc("GET /api/v1/members/{id}/metrics", s.api(s.memberMetrics))
	mux.HandleFunc("DELETE /api/v1/members/{id}", s.api(s.removeMember))
	mux.HandleFunc("GET /api/v1/teams", s.api(s.teams))
	mux.HandleFunc("POST /api/v1/teams", s.api(s.createTeam))
	mux.HandleFunc("POST /api/v1/invites", s.api(s.createInvite))
	mux.HandleFunc("GET /api/v1/grants", s.api(s.grants))
	mux.HandleFunc("POST /api/v1/grants", s.api(s.createGrant))
	mux.HandleFunc("DELETE /api/v1/grants/{id}", s.api(s.revokeGrant))
	mux.HandleFunc("GET /api/v1/audit", s.api(s.audit))
	mux.HandleFunc("PUT /api/v1/settings", s.api(s.settings))

	mux.HandleFunc("GET /static/app.css", s.css)
	mux.HandleFunc("GET /login", s.loginPage)
	mux.HandleFunc("POST /login", s.limitIP(s.loginSubmit))
	mux.HandleFunc("POST /logout", s.logout)
	mux.HandleFunc("GET /{$}", s.web(s.overviewPage))
	mux.HandleFunc("GET /me", s.web(s.mePage))
	mux.HandleFunc("GET /members", s.web(s.membersPage))
	mux.HandleFunc("GET /members/{id}", s.web(s.memberPage))
	mux.HandleFunc("GET /audit", s.web(s.auditPage))
	return s.recoverer(s.headers(s.logRequests(mux)))
}

func (s *Server) ready(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := s.store.Ping(ctx); err != nil {
		writeError(w, http.StatusServiceUnavailable, "database unreachable", "")
		return
	}
	_, _ = w.Write([]byte("ready\n"))
}

// headers sets the security headers every answer carries. Nothing is
// cached: every page holds figures about people.
func (s *Server) headers(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", "default-src 'none'; style-src 'self'; img-src 'self'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

// logRequests logs method, path, status and duration. Never the query
// string, which carries a login token on /login, and never a header.
func (s *Server) logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(sw, r)
		if r.URL.Path == "/healthz" || r.URL.Path == "/readyz" {
			return
		}
		s.log.Info("request", "method", r.Method, "path", r.URL.Path, "status", sw.status,
			"duration_ms", time.Since(start).Milliseconds())
	})
}

func (s *Server) recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				s.log.Error("panic", "path", r.URL.Path, "panic", v)
				writeError(w, http.StatusInternalServerError, "internal error", "")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func (s *Server) limitIP(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.byIP.Allow(clientIP(r, s.cfg.TrustedProxies)) {
			w.Header().Set("Retry-After", "10")
			writeError(w, http.StatusTooManyRequests, "too many requests", "wait a few seconds")
			return
		}
		h(w, r)
	}
}

// bearer is the request's bearer token, or "".
func bearer(r *http.Request) string {
	v, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok {
		return ""
	}
	return strings.TrimSpace(v)
}

// caller is who a request is from: a member, and the device when it came
// from one.
type caller struct {
	pgstore.Principal
	Device *pgstore.DeviceAuth
}

var errNoCredential = errors.New("no credential")

// authBearer resolves an API request's bearer token: a device token acts
// as its member, an admin token as its holder. Session cookies are not
// accepted on the API, so a page on another site cannot drive it.
func (s *Server) authBearer(r *http.Request) (caller, error) {
	tok := bearer(r)
	if tok == "" {
		return caller{}, errNoCredential
	}
	kind, err := team.KindOf(tok)
	if err != nil {
		return caller{}, pgstore.ErrUnauthorized
	}
	switch kind {
	case team.TokenDevice:
		d, err := s.store.AuthDevice(r.Context(), tok)
		if err != nil {
			return caller{}, err
		}
		p, err := s.store.Member(r.Context(), d.MemberID)
		if err != nil {
			return caller{}, pgstore.ErrUnauthorized
		}
		return caller{Principal: p, Device: &d}, nil
	case team.TokenAdmin:
		p, err := s.store.AuthToken(r.Context(), tok, team.TokenAdmin)
		return caller{Principal: p}, err
	}
	return caller{}, pgstore.ErrUnauthorized
}

// api wraps a JSON handler that needs a caller.
func (s *Server) api(h func(http.ResponseWriter, *http.Request, caller)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c, err := s.authBearer(r)
		if err != nil {
			s.authFailed(w, err)
			return
		}
		h(w, r, c)
	}
}

func (s *Server) authFailed(w http.ResponseWriter, err error) {
	if errors.Is(err, errNoCredential) || errors.Is(err, pgstore.ErrUnauthorized) || errors.Is(err, pgstore.ErrNotFound) {
		w.Header().Set("WWW-Authenticate", `Bearer realm="tokenops-team"`)
		writeError(w, http.StatusUnauthorized, "unauthorized", "send a device or admin token as a bearer token")
		return
	}
	s.internal(w, err)
}

func (s *Server) internal(w http.ResponseWriter, err error) {
	s.log.Error("request failed", "err", err)
	writeError(w, http.StatusInternalServerError, "internal error", "")
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg, hint string) {
	writeJSON(w, status, teamwire.Error{Error: msg, Hint: hint})
}

// decode reads a bounded JSON body, refusing unknown fields.
func decode(w http.ResponseWriter, r *http.Request, limit int64, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			writeError(w, http.StatusRequestEntityTooLarge, "request too large", "")
			return false
		}
		writeError(w, http.StatusBadRequest, "malformed request: "+err.Error(), "")
		return false
	}
	return true
}
