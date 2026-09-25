package dashauth

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newTestAuth(t *testing.T) *Authenticator {
	t.Helper()
	a, err := New(Config{AdminToken: "secret-token"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return a
}

func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
}

func TestNewRejectsMissingToken(t *testing.T) {
	if _, err := New(Config{}); err == nil {
		t.Fatal("expected error when no API token is set")
	}
}

func TestMiddlewareRequiresBearerToken(t *testing.T) {
	a := newTestAuth(t)
	cases := []struct {
		name   string
		token  string
		status int
	}{
		{name: "missing", status: http.StatusUnauthorized},
		{name: "wrong", token: "Bearer invalid", status: http.StatusUnauthorized},
		{name: "valid", token: "Bearer secret-token", status: http.StatusOK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/spend", nil)
			if tc.token != "" {
				req.Header.Set("Authorization", tc.token)
			}
			rec := httptest.NewRecorder()
			a.Middleware(okHandler()).ServeHTTP(rec, req)
			if rec.Code != tc.status {
				t.Fatalf("status = %d, want %d", rec.Code, tc.status)
			}
			if tc.status == http.StatusUnauthorized && !strings.Contains(rec.Header().Get("WWW-Authenticate"), "Bearer") {
				t.Errorf("missing WWW-Authenticate header: %v", rec.Header())
			}
		})
	}
}

func TestMiddlewareRejectsQueryToken(t *testing.T) {
	a := newTestAuth(t)
	req := httptest.NewRequest(http.MethodGet, "/api/spend?token=secret-token", nil)
	rec := httptest.NewRecorder()
	a.Middleware(okHandler()).ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("query token authenticated API request: status %d", rec.Code)
	}
}
