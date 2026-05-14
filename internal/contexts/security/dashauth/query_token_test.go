package dashauth

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Query-param auth: a request with ?token=… succeeds even without a
// bearer header. Used by the dashboard's first-click bootstrap so the
// MCP tool can hand the agent a clickable URL.
func TestAuthorizeAcceptsQueryToken(t *testing.T) {
	a, err := New(Config{AdminToken: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodGet, "/dashboard?token=secret", nil)
	if !a.authorize(r) {
		t.Fatal("authorize should accept matching query token")
	}
	r = httptest.NewRequest(http.MethodGet, "/dashboard?token=wrong", nil)
	if a.authorize(r) {
		t.Fatal("authorize must reject non-matching query token")
	}
}

// Browser bootstrap: query-param + Accept: text/html mints a cookie
// and 303s to the same URL without the token in the query string.
func TestMiddlewareBootstrapsBrowserSession(t *testing.T) {
	a, err := New(Config{AdminToken: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "ok")
	})
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/dashboard?token=secret&theme=dark", nil)
	r.Header.Set("Accept", "text/html")
	a.Middleware(next).ServeHTTP(rec, r)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d; want 303", rec.Code)
	}
	loc := rec.Header().Get("Location")
	if strings.Contains(loc, "token=") {
		t.Errorf("redirect must drop token query param: %q", loc)
	}
	if !strings.Contains(loc, "theme=dark") {
		t.Errorf("redirect must preserve other params: %q", loc)
	}
	cookies := rec.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("redirect must set session cookie")
	}
	// Cookie roundtrip works for follow-up requests.
	r2 := httptest.NewRequest(http.MethodGet, loc, nil)
	r2.AddCookie(cookies[0])
	rec2 := httptest.NewRecorder()
	a.Middleware(next).ServeHTTP(rec2, r2)
	if rec2.Code != http.StatusOK {
		t.Errorf("follow-up with cookie should 200; got %d", rec2.Code)
	}
}

// Non-browser query-param auth (e.g. curl) still works but does NOT
// redirect — the caller is scripted and expects a body response.
func TestMiddlewareScriptedQueryTokenServesBody(t *testing.T) {
	a, err := New(Config{AdminToken: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "ok")
	})
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/spend/summary?token=secret", nil)
	r.Header.Set("Accept", "application/json")
	a.Middleware(next).ServeHTTP(rec, r)
	if rec.Code != http.StatusOK {
		t.Errorf("non-browser request should serve body, got %d", rec.Code)
	}
	if rec.Body.String() != "ok" {
		t.Errorf("body = %q", rec.Body.String())
	}
}

// Missing credential of any kind → 401 with WWW-Authenticate so
// clients see they need to authenticate.
func TestMiddlewareRejectsAnonymousQueryToken(t *testing.T) {
	a, err := New(Config{AdminToken: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	a.Middleware(http.NotFoundHandler()).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/dashboard", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d; want 401", rec.Code)
	}
	if !strings.Contains(rec.Header().Get("WWW-Authenticate"), "Bearer") {
		t.Errorf("missing Bearer challenge: %q", rec.Header().Get("WWW-Authenticate"))
	}
}
