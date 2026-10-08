package accounts

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
)

// manusServer answers GetAvailableCredits for the session "sess" as Manus's
// Connect API does, sent as a bearer token.
func manusServer(t *testing.T, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer sess" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.Method != http.MethodPost || r.URL.Path != "/user.v1.UserService/GetAvailableCredits" ||
			r.Header.Get("Connect-Protocol-Version") != "1" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestManusReadsTheCredits(t *testing.T) {
	srv := manusServer(t, fixture(t, "manus"))
	for _, cred := range []string{"session_id=sess; other=x", "sess", "Cookie: other=x; Session_ID=sess"} {
		got, err := Manus{BaseURL: srv.URL}.Read(context.Background(), cred)
		if err != nil || !got.Subscription || len(got.Windows) != 2 {
			t.Fatalf("%q: %+v, %v", cred, got, err)
		}
		if !got.HasCredits || got.Credits != 3870 || got.CreditsUnit != "credits" || got.HasBalance {
			t.Errorf("balance %+v", got)
		}
		if w := got.Windows[0]; w.Name != "month" || !approx(w.UsedPct, 1000.0/39) || !w.ResetsAt.IsZero() {
			t.Errorf("month %+v", w)
		}
		if w := got.Windows[1]; w.Name != "day" || !approx(w.UsedPct, 50) || w.Duration != 24*time.Hour ||
			!w.ResetsAt.Equal(time.Date(2026, 10, 9, 0, 0, 0, 0, time.UTC)) {
			t.Errorf("day %+v", w)
		}
	}
}

// Manus's numeric refresh times count from 2001, inside a "data" envelope.
func TestManusEnvelopeAndReferenceDate(t *testing.T) {
	srv := manusServer(t, `{"data":{"totalCredits":"10","maxRefreshCredits":300,"refreshCredits":0,"nextRefreshTime":781228800}}`)
	got, err := Manus{BaseURL: srv.URL}.Read(context.Background(), "sess")
	if err != nil || len(got.Windows) != 1 || got.Credits != 10 {
		t.Fatalf("%+v %v", got, err)
	}
	if w := got.Windows[0]; !approx(w.UsedPct, 100) || !w.ResetsAt.Equal(time.Date(2025, 10, 4, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("day %+v", w)
	}
}

// An expired session is refused; a header without session_id is not sent.
func TestManusRefusesTheSession(t *testing.T) {
	srv := manusServer(t, fixture(t, "manus"))
	for _, cred := range []string{"session_id=expired", "other=x"} {
		if _, err := (Manus{BaseURL: srv.URL}).Read(context.Background(), cred); !errors.Is(err, usage.ErrAuth) {
			t.Errorf("%q = %v, want ErrAuth", cred, err)
		}
	}
}

// An answer with no credit field is an error, never zero credits.
func TestManusUnknownShape(t *testing.T) {
	srv := manusServer(t, `{"code":"internal","message":"boom"}`)
	if got, err := (Manus{BaseURL: srv.URL}).Read(context.Background(), "sess"); err == nil || errors.Is(err, usage.ErrAuth) {
		t.Errorf("read %+v, %v", got, err)
	}
}
