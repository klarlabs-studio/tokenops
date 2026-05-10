package dashauth

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func newTestAuth(t *testing.T) *Authenticator {
	t.Helper()
	a, err := New(Config{
		AdminPassword: "let-me-in",
		AdminToken:    "secret-token",
		SessionTTL:    time.Hour,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return a
}

func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(200)
		_, _ = w.Write([]byte("ok"))
	})
}

func TestNewRejectsNoCredentials(t *testing.T) {
	if _, err := New(Config{}); err == nil {
		t.Fatal("expected error when neither password nor token is set")
	}
}

func TestMiddlewareRejectsAnonymous(t *testing.T) {
	a := newTestAuth(t)
	rec := httptest.NewRecorder()
	a.Middleware(okHandler()).ServeHTTP(rec, httptest.NewRequest("GET", "/api/anything", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d", rec.Code)
	}
	if !strings.Contains(rec.Header().Get("WWW-Authenticate"), "Bearer") {
		t.Errorf("missing WWW-Authenticate: %v", rec.Header())
	}
}

func TestMiddlewareAcceptsBearerToken(t *testing.T) {
	a := newTestAuth(t)
	req := httptest.NewRequest("GET", "/api/spend", nil)
	req.Header.Set("Authorization", "Bearer secret-token")
	rec := httptest.NewRecorder()
	a.Middleware(okHandler()).ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Errorf("status = %d", rec.Code)
	}
}

func TestMiddlewareRejectsWrongBearer(t *testing.T) {
	a := newTestAuth(t)
	req := httptest.NewRequest("GET", "/api/spend", nil)
	req.Header.Set("Authorization", "Bearer not-the-token")
	rec := httptest.NewRecorder()
	a.Middleware(okHandler()).ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d", rec.Code)
	}
}

func TestLoginIssuesSessionCookie(t *testing.T) {
	a := newTestAuth(t)
	body := bytes.NewReader([]byte(`{"password":"let-me-in"}`))
	req := httptest.NewRequest("POST", "/dashboard/login", body)
	rec := httptest.NewRecorder()
	a.LoginHandler().ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("login status = %d, body = %q", rec.Code, rec.Body.String())
	}
	cookies := rec.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != "tokenops_session" {
		t.Fatalf("cookie missing: %v", cookies)
	}
	// Use the cookie on a follow-up request.
	req2 := httptest.NewRequest("GET", "/api/spend", nil)
	req2.AddCookie(cookies[0])
	rec2 := httptest.NewRecorder()
	a.Middleware(okHandler()).ServeHTTP(rec2, req2)
	if rec2.Code != 200 {
		t.Errorf("session-authenticated request status = %d", rec2.Code)
	}
}

func TestLoginRejectsWrongPassword(t *testing.T) {
	a := newTestAuth(t)
	body := bytes.NewReader([]byte(`{"password":"wrong"}`))
	req := httptest.NewRequest("POST", "/dashboard/login", body)
	rec := httptest.NewRecorder()
	a.LoginHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d", rec.Code)
	}
}

func TestLogoutInvalidatesSession(t *testing.T) {
	a := newTestAuth(t)
	// Login.
	body := bytes.NewReader([]byte(`{"password":"let-me-in"}`))
	rec := httptest.NewRecorder()
	a.LoginHandler().ServeHTTP(rec, httptest.NewRequest("POST", "/dashboard/login", body))
	cookie := rec.Result().Cookies()[0]

	// Logout.
	req := httptest.NewRequest("POST", "/dashboard/logout", nil)
	req.AddCookie(cookie)
	rec2 := httptest.NewRecorder()
	a.LogoutHandler().ServeHTTP(rec2, req)
	if rec2.Code != http.StatusNoContent {
		t.Errorf("logout status = %d", rec2.Code)
	}

	// Cookie no longer authenticates.
	req3 := httptest.NewRequest("GET", "/api/spend", nil)
	req3.AddCookie(cookie)
	rec3 := httptest.NewRecorder()
	a.Middleware(okHandler()).ServeHTTP(rec3, req3)
	if rec3.Code != http.StatusUnauthorized {
		t.Errorf("post-logout status = %d", rec3.Code)
	}
}

func TestReapDropsExpired(t *testing.T) {
	a, err := New(Config{
		AdminPassword: "let-me-in",
		SessionTTL:    1 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	body := bytes.NewReader([]byte(`{"password":"let-me-in"}`))
	rec := httptest.NewRecorder()
	a.LoginHandler().ServeHTTP(rec, httptest.NewRequest("POST", "/dashboard/login", body))
	if a.SessionCount() != 1 {
		t.Fatalf("session count = %d", a.SessionCount())
	}
	time.Sleep(5 * time.Millisecond)
	if dropped := a.Reap(); dropped != 1 {
		t.Errorf("reap dropped %d, want 1", dropped)
	}
	if a.SessionCount() != 0 {
		t.Errorf("session count after reap = %d", a.SessionCount())
	}
}

func TestPasswordLoginDisabledWhenUnset(t *testing.T) {
	a, _ := New(Config{AdminToken: "tk"})
	body := bytes.NewReader([]byte(`{"password":"x"}`))
	rec := httptest.NewRecorder()
	a.LoginHandler().ServeHTTP(rec, httptest.NewRequest("POST", "/dashboard/login", body))
	if rec.Code != http.StatusForbidden {
		t.Errorf("expected 403 when password disabled, got %d", rec.Code)
	}
}
