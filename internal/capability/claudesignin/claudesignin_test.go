package claudesignin

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func signedIn(t *testing.T, expires time.Time) string {
	t.Helper()
	home := t.TempDir()
	path := filepath.Join(home, ".claude", ".credentials.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	body := `{"claudeAiOauth":{"accessToken":"fake","expiresAt":` + strconv.FormatInt(expires.UnixMilli(), 10) + `,"scopes":["user:profile"],"subscriptionType":"max"}}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return home
}

// The check reports Anthropic's windows and the plan, a wait when asked
// to, and an account with nothing to meter as an error.
func TestCheck(t *testing.T) {
	now := time.Now()
	body, status := `{"seven_day":{"utilization":22,"resets_at":"2026-10-09T23:00:00Z"}}`, http.StatusOK
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if status == http.StatusTooManyRequests {
			w.Header().Set("Retry-After", "60")
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()
	o := Options{Home: signedIn(t, now.Add(time.Hour)), BaseURL: srv.URL, Now: now}

	r, err := Check(context.Background(), o)
	if err != nil || len(r.Summary) == 0 || r.Plan != "Max" {
		t.Fatalf("check = %+v, %v", r, err)
	}
	status = http.StatusTooManyRequests
	if r, err = Check(context.Background(), o); err != nil || r.RateLimitedUntil.IsZero() {
		t.Errorf("rate limited = %+v, %v", r, err)
	}
	status, body = http.StatusOK, `{}`
	if _, err = Check(context.Background(), o); !errors.Is(err, ErrNoWindows) {
		t.Errorf("no windows: %v", err)
	}
	o.Home = signedIn(t, now.Add(-time.Hour))
	if _, err = Check(context.Background(), o); err == nil {
		t.Error("an expired sign-in was accepted")
	}
}
