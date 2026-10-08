package teamserver

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"

	"go.klarlabs.de/tokenops/internal/contexts/team"
	"go.klarlabs.de/tokenops/internal/teamserver/pgstore"
)

// Single sign-on (ADR 0012 §4): OpenID Connect, authorization code with
// PKCE, a state bound to the browser by a cookie and stored server side
// with the nonce and verifier, and the ID token verified against the
// issuer's published keys (go-oidc). The verified e-mail address signs in
// the existing member it is set on; nobody is ever created.

// ssoTimeout bounds each call to an issuer: discovery, keys, the code
// exchange.
const ssoTimeout = 15 * time.Second

// providerTTL is how long an issuer's discovery document is reused. Its
// signing keys are refetched by go-oidc whenever a token names an unknown
// key.
const providerTTL = time.Hour

type cachedProvider struct {
	p  *oidc.Provider
	at time.Time
}

type providers struct {
	mu sync.Mutex
	m  map[string]cachedProvider
}

// provider discovers issuer, or reuses a recent discovery.
func (s *Server) provider(ctx context.Context, issuer string) (*oidc.Provider, error) {
	s.oidc.mu.Lock()
	c, ok := s.oidc.m[issuer]
	s.oidc.mu.Unlock()
	if ok && time.Since(c.at) < providerTTL {
		return c.p, nil
	}
	ctx, cancel := context.WithTimeout(oidc.ClientContext(ctx, s.httpClient), ssoTimeout)
	defer cancel()
	p, err := oidc.NewProvider(ctx, issuer)
	if err != nil {
		return nil, fmt.Errorf("discovering %s: %w", issuer, err)
	}
	s.oidc.mu.Lock()
	s.oidc.m[issuer] = cachedProvider{p: p, at: time.Now()}
	s.oidc.mu.Unlock()
	return p, nil
}

// CheckSSO discovers c's issuer and resolves its client secret, so the
// console can refuse a configuration that cannot work before storing it.
func CheckSSO(ctx context.Context, c team.SSO, client *http.Client) error {
	if client == nil {
		client = &http.Client{Timeout: ssoTimeout}
	}
	if _, err := clientSecret(c.SecretRef); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(oidc.ClientContext(ctx, client), ssoTimeout)
	defer cancel()
	p, err := oidc.NewProvider(ctx, c.Issuer)
	if err != nil {
		return fmt.Errorf("discovering %s: %w", c.Issuer, err)
	}
	var meta struct {
		Methods []string `json:"code_challenge_methods_supported"`
	}
	if err := p.Claims(&meta); err == nil && len(meta.Methods) > 0 {
		for _, m := range meta.Methods {
			if m == "S256" {
				return nil
			}
		}
		return fmt.Errorf("%s does not support PKCE with S256", c.Issuer)
	}
	return nil
}

// clientSecret reads the secret ref points at.
func clientSecret(ref string) (string, error) {
	kind, target, err := team.ParseSecretRef(ref)
	if err != nil {
		return "", err
	}
	var v string
	switch kind {
	case "env":
		v = os.Getenv(target)
	case "file":
		raw, err := os.ReadFile(target)
		if err != nil {
			return "", fmt.Errorf("client secret file: %w", err)
		}
		v = string(raw)
	}
	v = strings.TrimSpace(v)
	if v == "" {
		return "", fmt.Errorf("client secret %s is empty", ref)
	}
	return v, nil
}

func (s *Server) oauthConfig(p *oidc.Provider, c team.SSO) (*oauth2.Config, error) {
	secret, err := clientSecret(c.SecretRef)
	if err != nil {
		return nil, err
	}
	return &oauth2.Config{
		ClientID: c.ClientID, ClientSecret: secret, Endpoint: p.Endpoint(),
		RedirectURL: s.public.String() + "/sso/callback",
		Scopes:      []string{oidc.ScopeOpenID, "email", "profile"},
	}, nil
}

// ssoCookieName binds a sign-in in flight to the browser that started it.
// Lax, not Strict: the issuer sends the browser back with a cross-site
// navigation, which a Strict cookie would not accompany.
func (s *Server) ssoCookieName() string {
	if s.public.Scheme == "https" {
		return "__Host-tokenops_sso"
	}
	return "tokenops_sso"
}

func randomText() (string, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw[:]), nil
}

// ssoData is the SSO page's data.
type ssoData struct {
	Org    string
	Issuer string
	Start  string
	Error  string
}

// ssoPage names the organisation's issuer and links to it. A link, not a
// form: the form-action policy would stop a form's redirect to the issuer.
func (s *Server) ssoPage(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.URL.Query().Get("org"))
	if name == "" {
		s.render(w, http.StatusBadRequest, "signin", view{Title: "Sign in", Data: signinData{SSO: true, Error: "Enter your organisation's name."}})
		return
	}
	org, c, err := s.ssoFor(r.Context(), name)
	if err != nil {
		if errors.Is(err, pgstore.ErrNotFound) {
			s.render(w, http.StatusNotFound, "signin", view{Title: "Sign in",
				Data: signinData{SSO: true, Error: "No organisation of that name has single sign-on here."}})
			return
		}
		s.internal(w, err)
		return
	}
	host := c.Issuer
	if u, err := url.Parse(c.Issuer); err == nil {
		host = u.Host
	}
	s.render(w, http.StatusOK, "sso", view{Title: "Sign in", Data: ssoData{Org: org.Name, Issuer: host,
		Start: "/sso/start?org=" + url.QueryEscape(org.Name)}})
}

func (s *Server) ssoFor(ctx context.Context, orgName string) (pgstore.Org, team.SSO, error) {
	org, err := s.store.OrgByName(ctx, orgName)
	if err != nil {
		return org, team.SSO{}, err
	}
	c, err := s.store.SSOConfig(ctx, org.ID)
	return org, c, err
}

// ssoStart sends the browser to the issuer.
func (s *Server) ssoStart(w http.ResponseWriter, r *http.Request) {
	org, c, err := s.ssoFor(r.Context(), r.URL.Query().Get("org"))
	if errors.Is(err, pgstore.ErrNotFound) {
		s.render(w, http.StatusNotFound, "signin", view{Title: "Sign in",
			Data: signinData{SSO: true, Error: "No organisation of that name has single sign-on here."}})
		return
	}
	if err != nil {
		s.internal(w, err)
		return
	}
	p, err := s.provider(r.Context(), c.Issuer)
	if err != nil {
		s.ssoUnavailable(w, err)
		return
	}
	cfg, err := s.oauthConfig(p, c)
	if err != nil {
		s.ssoUnavailable(w, err)
		return
	}
	state, err := randomText()
	if err != nil {
		s.internal(w, err)
		return
	}
	nonce, err := randomText()
	if err != nil {
		s.internal(w, err)
		return
	}
	verifier := oauth2.GenerateVerifier()
	if err := s.store.BeginSSO(r.Context(), state, pgstore.SSOLogin{OrgID: org.ID, Nonce: nonce, Verifier: verifier}); err != nil {
		s.internal(w, err)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: s.ssoCookieName(), Value: state, Path: "/", HttpOnly: true,
		Secure: s.public.Scheme == "https", SameSite: http.SameSiteLaxMode, MaxAge: int(team.SSOStateTTL.Seconds())})
	http.Redirect(w, r, cfg.AuthCodeURL(state, oidc.Nonce(nonce), oauth2.S256ChallengeOption(verifier)), http.StatusFound)
}

func (s *Server) ssoUnavailable(w http.ResponseWriter, err error) {
	s.log.Error("single sign-on unavailable", "err", err)
	s.render(w, http.StatusBadGateway, "error", view{Title: "Single sign-on unavailable",
		Data: "Your organisation's identity provider could not be reached or is misconfigured. Use a sign-in link instead: run `tokenops team web`."})
}

// ssoRefused ends a sign-in that the issuer, or this server, refused.
func (s *Server) ssoRefused(w http.ResponseWriter, status int, msg string) {
	s.render(w, status, "error", view{Title: "Not signed in", Data: msg})
}

// ssoCallback finishes a sign-in: state, code exchange with the PKCE
// verifier, ID token signature, issuer, audience, expiry and nonce, then
// the e-mail address against the allowed domains and the members.
func (s *Server) ssoCallback(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	state := q.Get("state")
	ck, ckErr := r.Cookie(s.ssoCookieName())
	// The cookie has done its work whatever happens next.
	http.SetCookie(w, &http.Cookie{Name: s.ssoCookieName(), Value: "", Path: "/", HttpOnly: true,
		Secure: s.public.Scheme == "https", SameSite: http.SameSiteLaxMode, MaxAge: -1})
	if state == "" || ckErr != nil || subtle.ConstantTimeCompare([]byte(ck.Value), []byte(state)) != 1 {
		s.ssoRefused(w, http.StatusBadRequest, "This sign-in did not start in this browser, or it was already used. Start again from the sign-in page.")
		return
	}
	login, err := s.store.ConsumeSSO(r.Context(), state)
	if errors.Is(err, pgstore.ErrUnauthorized) {
		s.ssoRefused(w, http.StatusBadRequest, "This sign-in took longer than ten minutes or was already used. Start again from the sign-in page.")
		return
	}
	if err != nil {
		s.internal(w, err)
		return
	}
	if e := q.Get("error"); e != "" {
		s.ssoRefused(w, http.StatusForbidden, "Your identity provider did not sign you in ("+e+").")
		return
	}
	code := q.Get("code")
	if code == "" {
		s.ssoRefused(w, http.StatusBadRequest, "Your identity provider sent no authorization code.")
		return
	}
	c, err := s.store.SSOConfig(r.Context(), login.OrgID)
	if errors.Is(err, pgstore.ErrNotFound) {
		s.ssoRefused(w, http.StatusForbidden, "Single sign-on has been turned off for your organisation.")
		return
	}
	if err != nil {
		s.internal(w, err)
		return
	}
	claims, err := s.exchange(r.Context(), c, login, code)
	if err != nil {
		s.log.Warn("single sign-on refused", "org", login.OrgID, "err", err)
		s.ssoRefused(w, http.StatusForbidden, "Your identity provider's answer could not be verified. Start again, or use a sign-in link: run `tokenops team web`.")
		return
	}
	email, err := c.AcceptEmail(claims)
	if err != nil {
		s.ssoRefused(w, http.StatusForbidden, err.Error()+".")
		return
	}
	p, err := s.store.MemberByEmail(r.Context(), login.OrgID, email)
	if errors.Is(err, pgstore.ErrNotFound) {
		s.ssoRefused(w, http.StatusForbidden, team.ErrSSONoSuchEmail.Error()+".")
		return
	}
	if err != nil {
		s.internal(w, err)
		return
	}
	session, err := s.store.MintSession(r.Context(), p, "single sign-on "+c.Issuer)
	if err != nil {
		s.internal(w, err)
		return
	}
	s.setSession(w, session)
	// The browser arrived here from the issuer, cross-site, so a redirect
	// would not carry the new Strict cookie. A refresh from this page is a
	// same-site navigation, which does.
	s.render(w, http.StatusOK, "signedin", view{Title: "Signed in", Refresh: "/"})
}

// exchange trades code for tokens and verifies the ID token.
func (s *Server) exchange(ctx context.Context, c team.SSO, login pgstore.SSOLogin, code string) (team.SSOClaims, error) {
	p, err := s.provider(ctx, c.Issuer)
	if err != nil {
		return team.SSOClaims{}, err
	}
	cfg, err := s.oauthConfig(p, c)
	if err != nil {
		return team.SSOClaims{}, err
	}
	ctx, cancel := context.WithTimeout(oidc.ClientContext(ctx, s.httpClient), ssoTimeout)
	defer cancel()
	tok, err := cfg.Exchange(ctx, code, oauth2.VerifierOption(login.Verifier))
	if err != nil {
		return team.SSOClaims{}, fmt.Errorf("code exchange: %w", err)
	}
	raw, ok := tok.Extra("id_token").(string)
	if !ok || raw == "" {
		return team.SSOClaims{}, errors.New("no id_token in the token response")
	}
	idt, err := p.VerifierContext(ctx, &oidc.Config{ClientID: c.ClientID}).Verify(ctx, raw)
	if err != nil {
		return team.SSOClaims{}, fmt.Errorf("id token: %w", err)
	}
	if subtle.ConstantTimeCompare([]byte(idt.Nonce), []byte(login.Nonce)) != 1 {
		return team.SSOClaims{}, errors.New("id token: nonce does not match")
	}
	var cl struct {
		Email    string          `json:"email"`
		Verified json.RawMessage `json:"email_verified"`
	}
	if err := idt.Claims(&cl); err != nil {
		return team.SSOClaims{}, fmt.Errorf("id token claims: %w", err)
	}
	// Some issuers send email_verified as the string "true".
	v := strings.Trim(string(cl.Verified), `"`)
	return team.SSOClaims{Email: cl.Email, EmailVerified: v == "true"}, nil
}
