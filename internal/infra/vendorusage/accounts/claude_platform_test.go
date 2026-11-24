package accounts

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
)

const (
	claudeChatOrg     = "11111111-1111-4111-8111-111111111111"
	claudeAPIOrg      = "22222222-2222-4222-8222-222222222222"
	claudeOtherAPIOrg = "33333333-3333-4333-8333-333333333333"
)

// claudeConsole serves CodexBar's ClaudePlatformPluginTests fixtures:
// status by path overrides the 200s, and paths records what was asked.
func claudeConsole(t *testing.T, orgs string, status map[string]int, bodies map[string]string, paths *[]string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Cookie") != "sessionKey=sk-ant-sid-fixture" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		*paths = append(*paths, r.URL.Path)
		if s, ok := status[r.URL.Path]; ok {
			w.WriteHeader(s)
			return
		}
		if r.URL.Path == "/api/organizations" {
			_, _ = w.Write([]byte(orgs))
			return
		}
		if b, ok := bodies[r.URL.Path]; ok {
			_, _ = w.Write([]byte(b))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
}

const claudeOrgs = `[{"uuid":"` + claudeChatOrg + `","name":"Synthetic Chat Organization","capabilities":["chat","claude_max"]},
 {"uuid":"` + claudeAPIOrg + `","name":"Synthetic API Org","capabilities":["api","api_individual"]}]`

func TestClaudePlatformReadsTheAPIOrganisationsCreditAndSpend(t *testing.T) {
	var paths []string
	srv := claudeConsole(t, claudeOrgs, nil, map[string]string{
		"/api/organizations/" + claudeAPIOrg + "/prepaid/credits": fixture(t, "anthropic"),
		"/api/organizations/" + claudeAPIOrg + "/current_spend":   `{"amount":4321,"resets_at":"2026-11-01T00:00:00Z"}`,
	}, &paths)
	defer srv.Close()
	got, err := ClaudePlatform{BaseURL: srv.URL}.Read(context.Background(), "sessionKey=sk-ant-sid-fixture; other=1")
	if err != nil || !got.HasBalance || !approx(got.BalanceUSD, 187.25) || !got.HasUsed || !approx(got.UsedUSD, 43.21) ||
		got.Subscription || got.LimitUSD != 0 || got.Scope != "organization" {
		t.Fatalf("%+v %v", got, err)
	}
	// Billing is read for the API organisation, never the chat one listed first.
	want := []string{"/api/organizations", "/api/organizations/" + claudeAPIOrg + "/prepaid/credits", "/api/organizations/" + claudeAPIOrg + "/current_spend"}
	if len(paths) != len(want) {
		t.Fatalf("paths %v", paths)
	}
	for i := range want {
		if paths[i] != want[i] {
			t.Fatalf("paths %v", paths)
		}
	}
}

// A bare sessionKey value is sent as the cookie; a forbidden API
// organisation falls through to the next readable one.
func TestClaudePlatformForbiddenOrganisationFallsThrough(t *testing.T) {
	var paths []string
	orgs := `[{"uuid":"` + claudeAPIOrg + `","capabilities":["api"]},{"uuid":"` + claudeOtherAPIOrg + `","capabilities":["api"]}]`
	srv := claudeConsole(t, orgs, map[string]int{"/api/organizations/" + claudeAPIOrg + "/prepaid/credits": http.StatusForbidden},
		map[string]string{
			"/api/organizations/" + claudeOtherAPIOrg + "/prepaid/credits": `{"amount":1234,"currency":"USD","balance":{"credits":{"exponent":3}}}`,
		}, &paths)
	defer srv.Close()
	got, err := ClaudePlatform{BaseURL: srv.URL}.Read(context.Background(), "sk-ant-sid-fixture")
	// The spend failed (404): the balance stays, with a notice.
	if err != nil || !approx(got.BalanceUSD, 1.234) || got.HasUsed || got.Notice == "" {
		t.Fatalf("%+v %v", got, err)
	}
}

func TestClaudePlatformRefusals(t *testing.T) {
	for name, tc := range map[string]struct {
		orgs   string
		status map[string]int
	}{
		"signed out":          {claudeOrgs, map[string]int{"/api/organizations": http.StatusUnauthorized}},
		"no API organisation": {`[{"uuid":"` + claudeChatOrg + `","capabilities":["chat"]}]`, nil},
		"billing denied":      {claudeOrgs, map[string]int{"/api/organizations/" + claudeAPIOrg + "/prepaid/credits": http.StatusForbidden}},
	} {
		var paths []string
		srv := claudeConsole(t, tc.orgs, tc.status, nil, &paths)
		_, err := ClaudePlatform{BaseURL: srv.URL}.Read(context.Background(), "sessionKey=sk-ant-sid-fixture")
		srv.Close()
		if !errors.Is(err, usage.ErrAuth) {
			t.Errorf("%s: err %v", name, err)
		}
	}
}

// A malformed balance is an error, never $0; another currency is kept in
// it, unconverted.
func TestClaudePlatformBalanceShapes(t *testing.T) {
	for _, tc := range []struct {
		body    string
		ok      bool
		credits float64
		unit    string
	}{
		{`{"currency":"USD"}`, false, 0, ""},
		{`{"amount":"12","currency":"USD"}`, false, 0, ""},
		{`{"amount":12,"currency":"usd dollars"}`, false, 0, ""},
		{`{"amount":12,"balance":{"credits":{"exponent":7}}}`, false, 0, ""},
		{`{"amount":1250,"currency":"EUR"}`, true, 12.5, "EUR"},
	} {
		var paths []string
		srv := claudeConsole(t, claudeOrgs, nil, map[string]string{"/api/organizations/" + claudeAPIOrg + "/prepaid/credits": tc.body}, &paths)
		got, err := ClaudePlatform{BaseURL: srv.URL}.Read(context.Background(), "sessionKey=sk-ant-sid-fixture")
		srv.Close()
		if (err == nil) != tc.ok || (tc.ok && (!got.HasCredits || !approx(got.Credits, tc.credits) || got.CreditsUnit != tc.unit || got.HasBalance)) {
			t.Errorf("%s: %+v %v", tc.body, got, err)
		}
	}
}
