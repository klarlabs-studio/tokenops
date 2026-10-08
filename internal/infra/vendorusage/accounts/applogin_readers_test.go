package accounts

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// headerServer answers path with body when every header in want is set as
// given, and 401 otherwise.
func headerServer(t *testing.T, method, path string, want map[string]string, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for k, v := range want {
			if r.Header.Get(k) != v {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
		}
		if r.Method != method || r.URL.Path != path {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// Codebuff with the CLI's session also reads the weekly rate limit, which
// its API key cannot.
func TestCodebuffAppLoginReadsTheWeeklyLimit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer sess" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/api/v1/usage":
			_, _ = w.Write([]byte(fixture(t, "codebuff")))
		case "/api/user/subscription":
			_, _ = w.Write([]byte(`{"hasSubscription":true,"subscription":{"status":"active","tier":"pro"},"rateLimit":{"weeklyUsed":2100,"weeklyLimit":7000,"weeklyResetsAt":"2026-05-08T00:00:00Z"}}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	got, err := Codebuff{BaseURL: srv.URL}.ReadAppLogin(context.Background(), "sess")
	if err != nil || len(got.Windows) != 2 {
		t.Fatalf("%+v %v", got, err)
	}
	if w := got.Windows[1]; w.Name != "week" || !approx(w.UsedPct, 30) {
		t.Errorf("week %+v", w)
	}
}

// ClinePass sends Cline's own sign-in as a WorkOS token.
func TestClinePassAppLoginIsAWorkOSToken(t *testing.T) {
	srv := serve(t, "/api/v1/users/me/plan/usage-limits", "workos:at", fixture(t, "clinepass"))
	defer srv.Close()
	for _, tok := range []string{"at", "workos:at"} {
		if got, err := (ClinePass{BaseURL: srv.URL}).ReadAppLogin(context.Background(), tok); err != nil || len(got.Windows) == 0 {
			t.Errorf("%q: %+v %v", tok, got, err)
		}
	}
}
