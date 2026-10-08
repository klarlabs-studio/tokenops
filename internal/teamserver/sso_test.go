package teamserver_test

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"math/big"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/team"
	"go.klarlabs.de/tokenops/internal/teamserver/pgstore"
)

// fakeIssuer is an OpenID Connect provider for tests: discovery, an
// authorization endpoint that signs in whoever the test says, a token
// endpoint that checks the client secret and the PKCE verifier, and JWKS.
type fakeIssuer struct {
	t      *testing.T
	srv    *httptest.Server
	key    *rsa.PrivateKey
	other  *rsa.PrivateKey // signs forged tokens
	secret string

	mu sync.Mutex
	// codes maps an issued code to what its token answer says.
	codes map[string]issued
	// Next sign-in's claims and tampering.
	email     string
	verified  any
	nonce     string // overrides the request's nonce when set
	audience  string // overrides the client ID when set
	forge     bool   // sign with a key the issuer does not publish
	expired   bool
	pkceCalls int
}

type issued struct {
	nonce, challenge, redirect, clientID string
}

var (
	keysOnce      sync.Once
	keyA, keyB    *rsa.PrivateKey
	errIssuerKeys error
)

// issuerKeys are generated once: RSA key generation dominates otherwise.
func issuerKeys(t *testing.T) (*rsa.PrivateKey, *rsa.PrivateKey) {
	t.Helper()
	keysOnce.Do(func() {
		if keyA, errIssuerKeys = rsa.GenerateKey(rand.Reader, 2048); errIssuerKeys == nil {
			keyB, errIssuerKeys = rsa.GenerateKey(rand.Reader, 2048)
		}
	})
	if errIssuerKeys != nil {
		t.Fatal(errIssuerKeys)
	}
	return keyA, keyB
}

func newFakeIssuer(t *testing.T, secret string) *fakeIssuer {
	t.Helper()
	key, other := issuerKeys(t)
	f := &fakeIssuer{t: t, key: key, other: other, secret: secret, codes: map[string]issued{}, verified: true}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer": f.srv.URL, "authorization_endpoint": f.srv.URL + "/authorize", "token_endpoint": f.srv.URL + "/token",
			"jwks_uri": f.srv.URL + "/jwks", "response_types_supported": []string{"code"},
			"subject_types_supported": []string{"public"}, "id_token_signing_alg_values_supported": []string{"RS256"},
			"code_challenge_methods_supported": []string{"S256"},
		})
	})
	mux.HandleFunc("GET /jwks", func(w http.ResponseWriter, _ *http.Request) {
		b := func(v []byte) string { return base64.RawURLEncoding.EncodeToString(v) }
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{{
			"kty": "RSA", "kid": "k1", "use": "sig", "alg": "RS256",
			"n": b(key.N.Bytes()), "e": b(big.NewInt(int64(key.E)).Bytes()),
		}}})
	})
	mux.HandleFunc("GET /authorize", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("response_type") != "code" || q.Get("code_challenge_method") != "S256" || q.Get("code_challenge") == "" ||
			q.Get("nonce") == "" || q.Get("state") == "" || !strings.Contains(q.Get("scope"), "openid") {
			http.Error(w, "bad authorization request: "+r.URL.RawQuery, http.StatusBadRequest)
			return
		}
		code := "code-" + q.Get("state")[:8]
		f.mu.Lock()
		f.codes[code] = issued{nonce: q.Get("nonce"), challenge: q.Get("code_challenge"), redirect: q.Get("redirect_uri"), clientID: q.Get("client_id")}
		f.mu.Unlock()
		http.Redirect(w, r, q.Get("redirect_uri")+"?code="+code+"&state="+url.QueryEscape(q.Get("state")), http.StatusFound)
	})
	mux.HandleFunc("POST /token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		id, sec, ok := r.BasicAuth()
		if !ok {
			id, sec = r.PostForm.Get("client_id"), r.PostForm.Get("client_secret")
		}
		f.mu.Lock()
		is, found := f.codes[r.PostForm.Get("code")]
		delete(f.codes, r.PostForm.Get("code"))
		f.pkceCalls++
		f.mu.Unlock()
		sum := sha256.Sum256([]byte(r.PostForm.Get("code_verifier")))
		switch {
		case !found, sec != f.secret, id != is.clientID, r.PostForm.Get("redirect_uri") != is.redirect,
			base64.RawURLEncoding.EncodeToString(sum[:]) != is.challenge:
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "at", "token_type": "Bearer", "expires_in": 300,
			"id_token": f.idToken(is)})
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeIssuer) idToken(is issued) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	now := time.Now()
	nonce, aud := is.nonce, is.clientID
	if f.nonce != "" {
		nonce = f.nonce
	}
	if f.audience != "" {
		aud = f.audience
	}
	exp := now.Add(5 * time.Minute)
	if f.expired {
		exp = now.Add(-5 * time.Minute)
	}
	claims := map[string]any{"iss": f.srv.URL, "sub": "subject-1", "aud": aud, "iat": now.Unix(), "exp": exp.Unix(),
		"nonce": nonce, "email": f.email, "email_verified": f.verified}
	head, _ := json.Marshal(map[string]string{"alg": "RS256", "kid": "k1", "typ": "JWT"})
	body, _ := json.Marshal(claims)
	enc := base64.RawURLEncoding
	signing := enc.EncodeToString(head) + "." + enc.EncodeToString(body)
	digest := sha256.Sum256([]byte(signing))
	key := f.key
	if f.forge {
		key = f.other
	}
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		f.t.Fatal(err)
	}
	return signing + "." + enc.EncodeToString(sig)
}

// ssoEnv is a team server with SSO for Acme against a fake issuer.
type ssoEnv struct {
	*env
	idp   *fakeIssuer
	orgID string
}

func newSSOEnv(t *testing.T, allowUnverified bool) *ssoEnv {
	t.Helper()
	t.Setenv("TEAM_TEST_SSO_SECRET", "s3cret")
	e := newEnv(t)
	idp := newFakeIssuer(t, "s3cret")
	org, err := e.store.OrgByName(context.Background(), "Acme")
	if err != nil {
		t.Fatal(err)
	}
	if err := e.store.SetSSO(context.Background(), pgstore.Console(org.ID), team.SSO{Issuer: idp.srv.URL, ClientID: "team-plane",
		SecretRef: "env:TEAM_TEST_SSO_SECRET", Domains: []string{"acme.example"}, AllowUnverifiedEmail: allowUnverified}); err != nil {
		t.Fatal(err)
	}
	return &ssoEnv{env: e, idp: idp, orgID: org.ID}
}

// browser is a client that keeps cookies and does not follow redirects.
func browser() *http.Client {
	jar, _ := cookiejar.New(nil)
	return &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

// fetched is an answer whose body has been read and closed.
type fetched struct {
	StatusCode int
	Header     http.Header
}

func get(t *testing.T, c *http.Client, u string) (fetched, string) {
	t.Helper()
	resp, err := c.Get(u)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	return fetched{StatusCode: resp.StatusCode, Header: resp.Header}, string(body)
}

// signIn runs the whole flow in c as whoever the issuer is told to sign
// in, and returns the callback's answer.
func (e *ssoEnv) signIn(c *http.Client) (fetched, string) {
	e.t.Helper()
	resp, _ := get(e.t, c, e.srv.URL+"/sso/start?org=Acme")
	if resp.StatusCode != http.StatusFound || !strings.HasPrefix(resp.Header.Get("Location"), e.idp.srv.URL+"/authorize") {
		e.t.Fatalf("start: %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}
	resp, body := get(e.t, c, resp.Header.Get("Location"))
	if resp.StatusCode != http.StatusFound {
		e.t.Fatalf("authorize: %d %s", resp.StatusCode, body)
	}
	callback := resp.Header.Get("Location")
	if !strings.HasPrefix(callback, e.srv.URL+"/sso/callback?") {
		e.t.Fatalf("issuer sent the browser to %s", callback)
	}
	return get(e.t, c, callback)
}

func (e *ssoEnv) signedIn(c *http.Client) bool {
	e.t.Helper()
	resp, _ := get(e.t, c, e.srv.URL+"/")
	return resp.StatusCode == http.StatusOK
}

func TestSSOSignsInTheMemberTheVerifiedEmailIsSetOn(t *testing.T) {
	e := newSSOEnv(t, false)
	ann := e.join("platform", "Ann", team.RoleMember)
	if code, body := e.do("PUT", "/api/v1/members/"+ann.MemberID+"/email", e.admin, map[string]string{"email": "Ann@Acme.example"}); code != http.StatusOK {
		t.Fatalf("set email: %d %s", code, body)
	}
	e.idp.email = "ann@acme.example"
	c := browser()
	resp, body := e.signIn(c)
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, `http-equiv="refresh"`) {
		t.Fatalf("callback: %d %s", resp.StatusCode, body)
	}
	if !e.signedIn(c) {
		t.Fatal("no session after a verified sign-in")
	}
	// The member's page shows the address that signs them in.
	_, me := get(t, c, e.srv.URL+"/me")
	if !strings.Contains(me, "ann@acme.example") || !strings.Contains(me, "Ann") {
		t.Errorf("/me does not show the SSO address or is not Ann's: %s", me)
	}
	// The sign-in is in the audit log.
	_, audit := e.do("GET", "/api/v1/audit", e.admin, nil)
	if !strings.Contains(string(audit), `"member.signed_in"`) || !strings.Contains(string(audit), `"member.email_set"`) {
		t.Errorf("audit lacks the sign-in or the address change: %s", audit)
	}
	// The callback cannot be replayed: the state was used.
	if e.idp.pkceCalls != 1 {
		t.Errorf("token endpoint called %d times", e.idp.pkceCalls)
	}
}

func TestSSORefusals(t *testing.T) {
	cases := []struct {
		name   string
		setup  func(e *ssoEnv)
		status int
	}{
		{"no member has the address", func(e *ssoEnv) { e.idp.email = "nobody@acme.example" }, http.StatusForbidden},
		{"domain not allowed", func(e *ssoEnv) { e.idp.email = "ann@evil.example" }, http.StatusForbidden},
		{"address not verified", func(e *ssoEnv) { e.idp.verified = false }, http.StatusForbidden},
		{"verified as a string other than true", func(e *ssoEnv) { e.idp.verified = "false" }, http.StatusForbidden},
		{"nonce replaced", func(e *ssoEnv) { e.idp.nonce = "attacker-nonce" }, http.StatusForbidden},
		{"token for another client", func(e *ssoEnv) { e.idp.audience = "some-other-app" }, http.StatusForbidden},
		{"token signed with an unpublished key", func(e *ssoEnv) { e.idp.forge = true }, http.StatusForbidden},
		{"token expired", func(e *ssoEnv) { e.idp.expired = true }, http.StatusForbidden},
		{"wrong client secret", func(e *ssoEnv) { e.idp.secret = "other" }, http.StatusForbidden},
		{"member removed", func(e *ssoEnv) {
			m, _ := e.store.MemberByEmail(context.Background(), e.orgID, "ann@acme.example")
			if err := e.store.RemoveMember(context.Background(), pgstore.Console(e.orgID), m.MemberID); err != nil {
				e.t.Fatal(err)
			}
		}, http.StatusForbidden},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newSSOEnv(t, false)
			ann := e.join("platform", "Ann", team.RoleMember)
			if err := e.store.SetEmail(context.Background(), pgstore.Console(e.orgID), ann.MemberID, "ann@acme.example"); err != nil {
				t.Fatal(err)
			}
			e.idp.email = "ann@acme.example"
			tc.setup(e)
			c := browser()
			resp, body := e.signIn(c)
			if resp.StatusCode != tc.status {
				t.Errorf("callback: %d, want %d: %s", resp.StatusCode, tc.status, body)
			}
			if e.signedIn(c) {
				t.Error("signed in anyway")
			}
		})
	}
}

func TestSSOUnverifiedEmailWhenTheOrgAllowsIt(t *testing.T) {
	e := newSSOEnv(t, true)
	ann := e.join("platform", "Ann", team.RoleMember)
	if err := e.store.SetEmail(context.Background(), pgstore.Console(e.orgID), ann.MemberID, "ann@acme.example"); err != nil {
		t.Fatal(err)
	}
	e.idp.email, e.idp.verified = "ann@acme.example", nil // Entra sends no email_verified
	c := browser()
	if resp, body := e.signIn(c); resp.StatusCode != http.StatusOK {
		t.Fatalf("callback: %d %s", resp.StatusCode, body)
	}
	if !e.signedIn(c) {
		t.Error("not signed in")
	}
}

func TestSSOStateIsBoundToTheBrowserAndSingleUse(t *testing.T) {
	e := newSSOEnv(t, false)
	ann := e.join("platform", "Ann", team.RoleMember)
	if err := e.store.SetEmail(context.Background(), pgstore.Console(e.orgID), ann.MemberID, "ann@acme.example"); err != nil {
		t.Fatal(err)
	}
	e.idp.email = "ann@acme.example"

	// Login CSRF: the attacker starts a sign-in and sends the victim the
	// callback link. The victim's browser has no state cookie.
	attacker := browser()
	resp, _ := get(t, attacker, e.srv.URL+"/sso/start?org=Acme")
	resp, _ = get(t, attacker, resp.Header.Get("Location"))
	callback := resp.Header.Get("Location")
	victim := browser()
	if resp, _ := get(t, victim, callback); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("callback in another browser: %d", resp.StatusCode)
	}
	if e.signedIn(victim) {
		t.Error("victim signed in as the attacker's account")
	}

	// Replay: the same callback twice in the same browser.
	c := browser()
	resp, _ = get(t, c, e.srv.URL+"/sso/start?org=Acme")
	resp, _ = get(t, c, resp.Header.Get("Location"))
	callback = resp.Header.Get("Location")
	if resp, body := get(t, c, callback); resp.StatusCode != http.StatusOK {
		t.Fatalf("first callback: %d %s", resp.StatusCode, body)
	}
	// Put the state cookie back, as an attacker holding the URL would.
	u, _ := url.Parse(callback)
	c.Jar.SetCookies(u, []*http.Cookie{{Name: "tokenops_sso", Value: u.Query().Get("state"), Path: "/"}})
	if resp, _ := get(t, c, callback); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("replayed callback: %d", resp.StatusCode)
	}

	// An issuer error is shown, and signs nobody in.
	c = browser()
	resp, _ = get(t, c, e.srv.URL+"/sso/start?org=Acme")
	loc, _ := url.Parse(resp.Header.Get("Location"))
	resp, body := get(t, c, e.srv.URL+"/sso/callback?error=access_denied&state="+url.QueryEscape(loc.Query().Get("state")))
	if resp.StatusCode != http.StatusForbidden || !strings.Contains(body, "access_denied") || e.signedIn(c) {
		t.Errorf("issuer error: %d %s", resp.StatusCode, body)
	}
}

func TestSSOPagesAndLinkFallback(t *testing.T) {
	e := newSSOEnv(t, false)
	c := browser()
	_, body := get(t, c, e.srv.URL+"/")
	if !strings.Contains(body, `action="/sso"`) || !strings.Contains(body, "tokenops team web") {
		t.Errorf("sign-in page offers neither SSO nor links: %s", body)
	}
	resp, body := get(t, c, e.srv.URL+"/sso?org=Acme")
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, "/sso/start?org=Acme") {
		t.Errorf("sso page: %d %s", resp.StatusCode, body)
	}
	if resp, _ := get(t, c, e.srv.URL+"/sso?org=Nobody"); resp.StatusCode != http.StatusNotFound {
		t.Errorf("unknown org: %d", resp.StatusCode)
	}
	// Single-use links keep working beside SSO.
	resp2, err := c.PostForm(e.srv.URL+"/login", url.Values{"t": {e.login}})
	if err != nil {
		t.Fatal(err)
	}
	_ = resp2.Body.Close()
	if resp2.StatusCode != http.StatusSeeOther {
		t.Fatalf("link sign-in: %d", resp2.StatusCode)
	}
	if !e.signedIn(c) {
		t.Error("link sign-in did not start a session")
	}
}

func TestSetEmailIsForOwners(t *testing.T) {
	e := newSSOEnv(t, false)
	ann := e.join("platform", "Ann", team.RoleMember)
	adm := e.join("platform", "Adam", team.RoleAdmin)
	other := e.join("platform", "Otto", team.RoleOwner)
	admTok, err := e.store.MintAdminToken(context.Background(), pgstore.Principal{MemberID: adm.MemberID, OrgID: e.orgID, Role: team.RoleAdmin, DisplayName: "Adam"})
	if err != nil {
		t.Fatal(err)
	}
	if code, _ := e.do("PUT", "/api/v1/members/"+ann.MemberID+"/email", admTok, map[string]string{"email": "adam@acme.example"}); code != http.StatusForbidden {
		t.Errorf("an admin bound a member to an address: %d", code)
	}
	if code, _ := e.do("PUT", "/api/v1/members/"+ann.MemberID+"/email", ann.DeviceToken, map[string]string{"email": "ann@acme.example"}); code != http.StatusForbidden {
		t.Errorf("a device set an address: %d", code)
	}
	if code, _ := e.do("PUT", "/api/v1/members/"+other.MemberID+"/email", e.admin, map[string]string{"email": "olivia@acme.example"}); code != http.StatusForbidden {
		t.Errorf("an owner bound another owner to an address: %d", code)
	}
	if code, _ := e.do("PUT", "/api/v1/members/"+ann.MemberID+"/email", e.admin, map[string]string{"email": "ann@acme.example"}); code != http.StatusOK {
		t.Errorf("owner: %d", code)
	}
	if code, _ := e.do("PUT", "/api/v1/members/"+adm.MemberID+"/email", e.admin, map[string]string{"email": "ANN@acme.example"}); code != http.StatusConflict {
		t.Errorf("two members with one address: %d", code)
	}
	if code, _ := e.do("PUT", "/api/v1/members/"+ann.MemberID+"/email", e.admin, map[string]string{"email": "not an address"}); code != http.StatusBadRequest {
		t.Errorf("malformed address: %d", code)
	}
}
