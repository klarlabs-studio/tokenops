package accounts

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
)

// t3ChatServer answers getCustomerData for the cookie "sid=ok" as
// t3.chat's tRPC batch link does: JSONL, the data on a later line.
func t3ChatServer(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Cookie") != "sid=ok; theme=dark" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.URL.Path != "/api/trpc/getCustomerData" || r.URL.Query().Get("batch") != "1" ||
			!strings.Contains(r.URL.Query().Get("input"), "sessionId") || r.Header.Get("Origin") != "https://t3.chat" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestT3ChatReadsTheWindows(t *testing.T) {
	srv := t3ChatServer(t, http.StatusOK, `{"json":{"0":[[0],[null,0,0]]}}`+"\n"+fixture(t, "t3chat"))
	got, err := T3Chat{BaseURL: srv.URL}.Read(context.Background(), "Cookie: sid=ok; theme=dark")
	if err != nil || !got.Subscription || len(got.Windows) != 2 {
		t.Fatalf("%+v, %v", got, err)
	}
	if w := got.Windows[0]; w.Name != "4h" || w.Duration != 4*time.Hour || !approx(w.UsedPct, 42.5) ||
		!w.ResetsAt.Equal(time.Date(2026, 10, 7, 11, 0, 0, 0, time.UTC)) {
		t.Errorf("4h %+v", w)
	}
	if w := got.Windows[1]; w.Name != "month" || !approx(w.UsedPct, 12) ||
		!w.ResetsAt.Equal(time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("month %+v", w)
	}
}

func TestT3ChatRefusals(t *testing.T) {
	srv := t3ChatServer(t, http.StatusOK, fixture(t, "t3chat"))
	for _, cred := range []string{"sid=expired", "just-a-token"} {
		if _, err := (T3Chat{BaseURL: srv.URL}).Read(context.Background(), cred); !errors.Is(err, usage.ErrAuth) {
			t.Errorf("%q = %v, want ErrAuth", cred, err)
		}
	}
	challenge := t3ChatServer(t, http.StatusTooManyRequests, "")
	if _, err := (T3Chat{BaseURL: challenge.URL}).Read(context.Background(), "sid=ok; theme=dark"); err == nil ||
		errors.Is(err, usage.ErrAuth) || !strings.Contains(err.Error(), "browser check") {
		t.Errorf("challenge = %v", err)
	}
}

func TestT3ChatUnknownShape(t *testing.T) {
	srv := t3ChatServer(t, http.StatusOK, `{"json":{"ok":true}}`)
	if got, err := (T3Chat{BaseURL: srv.URL}).Read(context.Background(), "sid=ok; theme=dark"); err == nil {
		t.Errorf("read %+v", got)
	}
}
