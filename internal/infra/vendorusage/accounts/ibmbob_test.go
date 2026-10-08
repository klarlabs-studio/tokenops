package accounts

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
)

// ibmBobServer serves the profile and each team's budget from the fixture
// for the authorization want.
func ibmBobServer(t *testing.T, want string, profile json.RawMessage, budgets map[string]json.RawMessage) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != want {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.URL.Path == "/admin/v1/profile" {
			_, _ = w.Write(profile)
			return
		}
		team, ok := strings.CutPrefix(r.URL.Path, "/admin/v1/teams/")
		team, user, _ := strings.Cut(team, "/users/")
		if !ok || user != "user-1" || r.Header.Get("X-Team-Id") != team || r.Header.Get("X-Instance-Id") != "inst-1" || budgets[team] == nil {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write(budgets[team])
	}))
	t.Cleanup(srv.Close)
	return srv
}

func ibmBobFixture(t *testing.T) (json.RawMessage, map[string]json.RawMessage) {
	t.Helper()
	var f struct {
		Profile json.RawMessage            `json:"profile"`
		Budgets map[string]json.RawMessage `json:"budgets"`
	}
	if err := json.Unmarshal([]byte(fixture(t, "ibmbob")), &f); err != nil {
		t.Fatal(err)
	}
	return f.Profile, f.Budgets
}

func TestIBMBobSumsTheTeams(t *testing.T) {
	profile, budgets := ibmBobFixture(t)
	srv := ibmBobServer(t, "Apikey bk", profile, budgets)
	got, err := IBMBob{BaseURL: srv.URL}.Read(context.Background(), "bk")
	if err != nil || !got.Subscription || len(got.Windows) != 1 {
		t.Fatalf("got %+v, %v", got, err)
	}
	// 300 + 150 Bobcoins of 1,000 + 500 (team-b's budget from the profile).
	if w := got.Windows[0]; w.Name != "month" || !approx(w.UsedPct, 30) || !w.ResetsAt.Equal(time.Unix(1793491200, 0)) {
		t.Errorf("month %+v", w)
	}
}

func TestIBMBobSendsAnIAMTokenAsBearer(t *testing.T) {
	profile, budgets := ibmBobFixture(t)
	jwt := "eyJhbGciOiJSUzI1NiJ9." + base64.RawURLEncoding.EncodeToString([]byte(`{"sub":"x"}`)) + ".sig"
	srv := ibmBobServer(t, "Bearer "+jwt, profile, budgets)
	if _, err := (IBMBob{BaseURL: srv.URL}).Read(context.Background(), jwt); err != nil {
		t.Errorf("an IAM token = %v", err)
	}
}

func TestIBMBobRefusedUnknownAndUntrusted(t *testing.T) {
	profile, budgets := ibmBobFixture(t)
	srv := ibmBobServer(t, "Apikey bk", profile, budgets)
	if _, err := (IBMBob{BaseURL: srv.URL}).Read(context.Background(), "bad"); !errors.Is(err, usage.ErrAuth) {
		t.Errorf("a refused key = %v, want ErrAuth", err)
	}
	none := ibmBobServer(t, "Apikey bk", json.RawMessage(`{"instances":[]}`), nil)
	if got, err := (IBMBob{BaseURL: none.URL}).Read(context.Background(), "bk"); err != nil || !got.Empty() {
		t.Errorf("no instances = %+v, %v", got, err)
	}
	for _, domain := range []string{"evil.example", "bob.ibm.com.evil.example", "us-east.bob.ibm.com:8443"} {
		if _, err := (IBMBob{}).regionalBase(domain); err == nil {
			t.Errorf("%s was trusted", domain)
		}
	}
	if got, err := (IBMBob{}).regionalBase("eu-de.bob.ibm.com"); err != nil || got != "https://api.eu-de.bob.ibm.com" {
		t.Errorf("eu-de = %q, %v", got, err)
	}
}
