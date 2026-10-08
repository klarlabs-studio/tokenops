package accounts

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
)

const devinToken = "auth1_0123456789abcdefghijklmnop"

// devinServer answers the quota route for one organisation, as app.devin.ai
// does for the token devinToken.
func devinServer(t *testing.T, path string) (*httptest.Server, *[]string) {
	t.Helper()
	var asked []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked = append(asked, r.URL.Path)
		if r.Header.Get("Authorization") != "Bearer "+devinToken {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.URL.Path != path {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(fixture(t, "devin")))
	}))
	t.Cleanup(srv.Close)
	return srv, &asked
}

// The bundle setup reads from localStorage: the auth1 session and the
// organisation last used.
func TestDevinReadsWithTheLocalStorageSession(t *testing.T) {
	srv, asked := devinServer(t, "/api/org-abc123/billing/quota/usage")
	bundle := `{"@@devin@@::auth1_session":"{\"token\":\"` + devinToken + `\"}","last-internal-org-for-external-org-v1-acme":"\"org-abc123\""}`
	got, err := Devin{BaseURL: srv.URL}.Read(context.Background(), bundle)
	if err != nil || !got.Subscription || len(got.Windows) != 2 || !got.HasBalance || !approx(got.BalanceUSD, 12.5) {
		t.Fatalf("%+v %v", got, err)
	}
	if w := got.Windows[0]; w.Name != "day" || !approx(w.UsedPct, 12) || w.ResetsAt.IsZero() {
		t.Errorf("day %+v", w)
	}
	if w := got.Windows[1]; w.Name != "week" || !approx(w.UsedPct, 42) {
		t.Errorf("week %+v", w)
	}
	if len(*asked) != 1 {
		t.Errorf("asked %v", *asked)
	}
}

// A pasted token with the organisation's slug falls back from the
// internal-ID route to the slug route.
func TestDevinPastedSessionBySlug(t *testing.T) {
	srv, asked := devinServer(t, "/api/org/acme/billing/quota/usage")
	if got, err := (Devin{BaseURL: srv.URL}).Read(context.Background(), "Bearer "+devinToken+":acme"); err != nil || len(got.Windows) != 2 {
		t.Fatalf("%+v %v", got, err)
	}
	if len(*asked) != 1 || (*asked)[0] != "/api/org/acme/billing/quota/usage" {
		t.Errorf("asked %v", *asked)
	}
}

func TestDevinRefusals(t *testing.T) {
	srv, _ := devinServer(t, "/api/org-abc123/billing/quota/usage")
	for _, c := range []string{"", "no-org-given", `{"other":"x"}`, "auth1_wrong:org-abc123"} {
		if _, err := (Devin{BaseURL: srv.URL}).Read(context.Background(), c); !errors.Is(err, usage.ErrAuth) {
			t.Errorf("%q: %v", c, err)
		}
	}
	bundle := `{"x_auth1_session":"{\"token\":\"` + devinToken + `\"}"}`
	if _, err := (Devin{BaseURL: srv.URL}).Read(context.Background(), bundle); err == nil {
		t.Error("a session with no organisation was read")
	}
}
