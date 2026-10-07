package proxy

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// headerEquals accepts exactly one Authorization header value, standing in
// for dashauth.Authenticator (whose own tests cover the token comparison).
type headerEquals string

func (h headerEquals) AuthorizeHeader(authorization string) bool {
	return authorization == string(h)
}

func bearerOKHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
}

func TestBearerAuthRequiresBearerToken(t *testing.T) {
	auth := BearerAuth(headerEquals("Bearer secret-token"))
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
			auth.Middleware(bearerOKHandler()).ServeHTTP(rec, req)
			if rec.Code != tc.status {
				t.Fatalf("status = %d, want %d", rec.Code, tc.status)
			}
			if tc.status == http.StatusUnauthorized && !strings.Contains(rec.Header().Get("WWW-Authenticate"), "Bearer") {
				t.Errorf("missing WWW-Authenticate header: %v", rec.Header())
			}
		})
	}
}

func TestBearerAuthRejectsQueryToken(t *testing.T) {
	auth := BearerAuth(headerEquals("Bearer secret-token"))
	req := httptest.NewRequest(http.MethodGet, "/api/spend?token=secret-token", nil)
	rec := httptest.NewRecorder()
	auth.Middleware(bearerOKHandler()).ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("query token authenticated API request: status %d", rec.Code)
	}
}
