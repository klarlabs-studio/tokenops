package fireworks

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/fireworks"
)

// The shapes Fireworks answers with for an ordinary member's key.
const (
	usersAda = `{"users":[{"name":"accounts/acme/users/u-ada","email":"ada@example.com","role":"user","state":"READY"}],"totalSize":1}`
	// limitsBlocked is a member over their cap, blocked to month's end.
	limitsBlocked = `{"effectiveLimit":{"currencyCode":"USD","nanos":0,"units":"100"},"effectiveLimitSource":"ACCOUNT_DEFAULT","effectiveLimitSourceGroups":[],"enforcementMode":"BLOCK","exceededUntil":"2026-11-01T00:00:00Z","limitOverride":null,"name":"accounts/acme/users/u-ada/usageLimits","updateTime":null,"used":{"currencyCode":"USD","nanos":336969442,"units":"105"}}`
)

// nexusAPI serves the users list and one member's limits. users is the
// list body; limits the member's limits ("" for a 500).
func nexusAPI(t *testing.T, users, limits string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer fw_test" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/verifyApiKey":
			w.Header().Set("x-fireworks-account-id", "acme")
		case "/v1/accounts/acme/users":
			if r.URL.Query().Get("filter") != `email="ada@example.com"` {
				t.Errorf("filter %q", r.URL.Query().Get("filter"))
			}
			_, _ = w.Write([]byte(users))
		case "/v1/accounts/gone/users":
			http.Redirect(w, r, "/login", http.StatusTemporaryRedirect)
		case "/v1/accounts/other/users":
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"code":7}`))
		case "/v1/accounts/acme/users/u-ada/usageLimits":
			if limits == "" {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			_, _ = w.Write([]byte(limits))
		case "/v1/accounts/acme/billing/summary":
			_, _ = w.Write([]byte(`{"lineItems":[{"totalCost":{"currencyCode":"USD","units":"900"}}]}`))
		case "/v1/accounts/acme/quotas":
			_, _ = w.Write([]byte(`{"quotas":[]}`))
		case "/v1/accounts/acme":
			_, _ = w.Write([]byte(`{"name":"accounts/acme"}`))
		case "/login":
			_, _ = w.Write([]byte(`<html>sign in</html>`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

func TestFindUserByEmail(t *testing.T) {
	srv := nexusAPI(t, usersAda, limitsBlocked)
	defer srv.Close()
	account, user, err := client(srv).FindUser(context.Background(), "", "ada@example.com")
	if err != nil || account != "acme" || user != "u-ada" {
		t.Errorf("got %q %q %v", account, user, err)
	}
}

// No member, several, or only a service account: no single user.
func TestFindUserNeedsExactlyOneMember(t *testing.T) {
	for name, body := range map[string]string{
		"none":    `{"users":[],"totalSize":0}`,
		"empty":   `{}`,
		"several": `{"users":[{"name":"accounts/acme/users/a","email":"ada@example.com"},{"name":"accounts/acme/users/b","email":"ada@example.com"}]}`,
		"service": `{"users":[{"name":"accounts/acme/users/bot","email":"ada@example.com","serviceAccount":true}]}`,
		"other":   `{"users":[{"name":"accounts/acme/users/b","email":"bob@example.com"}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			srv := nexusAPI(t, body, "")
			defer srv.Close()
			if _, u, err := client(srv).FindUser(context.Background(), "acme", "ada@example.com"); !errors.Is(err, usage.ErrNoUserMatch) || u != "" {
				t.Errorf("got %q %v", u, err)
			}
		})
	}
	srv := nexusAPI(t, `{"users":[{"name":"accounts/acme/users/bot","email":"ada@example.com","serviceAccount":true},{"name":"accounts/acme/users/u-ada","email":"Ada@Example.com"}]}`, "")
	defer srv.Close()
	if _, u, err := client(srv).FindUser(context.Background(), "acme", "ada@example.com"); err != nil || u != "u-ada" {
		t.Errorf("a service account beside the member: %q %v", u, err)
	}
}

// An unknown account redirects and a wrong one is refused; both carry
// their status instead of following the redirect to a page.
func TestFindUserCarriesTheStatus(t *testing.T) {
	srv := nexusAPI(t, usersAda, "")
	defer srv.Close()
	for account, want := range map[string]int{"gone": 307, "other": 403} {
		_, _, err := client(srv).FindUser(context.Background(), account, "ada@example.com")
		if usage.StatusOf(err) != want {
			t.Errorf("%s: err %v, want status %d", account, err, want)
		}
	}
}

// The stamp names the key without revealing it, and changes with it.
func TestKeyStamp(t *testing.T) {
	a, _ := (&Client{Key: func(context.Context) (string, error) { return "fw_one", nil }}).KeyStamp(context.Background())
	b, _ := (&Client{Key: func(context.Context) (string, error) { return "fw_two", nil }}).KeyStamp(context.Background())
	if a == "" || a == b || strings.Contains(a, "fw_") || strings.Contains(a, "one") {
		t.Errorf("stamps %q %q", a, b)
	}
}

// A member over their cap is blocked until exceededUntil, as Fireworks
// says it, and the reading carries both.
func TestReadCarriesTheBlock(t *testing.T) {
	srv := nexusAPI(t, usersAda, limitsBlocked)
	defer srv.Close()
	at := time.Date(2026, 10, 27, 9, 0, 0, 0, time.UTC)
	r, err := client(srv).Read(context.Background(), "acme", "u-ada", at)
	if err != nil {
		t.Fatal(err)
	}
	if r.Scope != usage.ScopeUser || r.LimitUSD != 100 || r.UsedUSD < 105.33 || r.UsedUSD > 105.34 {
		t.Errorf("reading %+v", r)
	}
	if !r.LimitReached || !r.BlockedUntil.Equal(time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("block %v until %v", r.LimitReached, r.BlockedUntil)
	}
	// A past exceededUntil, a null one, or one not enforced is no block.
	for name, body := range map[string]string{
		"past":    strings.Replace(limitsBlocked, "2026-11-01", "2026-10-01", 1),
		"null":    strings.Replace(limitsBlocked, `"2026-11-01T00:00:00Z"`, "null", 1),
		"no mode": strings.Replace(limitsBlocked, `"BLOCK"`, `"WARN"`, 1),
	} {
		srv := nexusAPI(t, usersAda, body)
		r, err := client(srv).Read(context.Background(), "acme", "u-ada", at)
		srv.Close()
		if err != nil || r.LimitReached || !r.BlockedUntil.IsZero() {
			t.Errorf("%s: %+v %v", name, r, err)
		}
	}
}

// When the member's own read fails or they have no cap, the account's
// month is the reading, and it says why.
func TestReadSaysWhyItFellBack(t *testing.T) {
	srv := nexusAPI(t, usersAda, "")
	defer srv.Close()
	r, err := client(srv).Read(context.Background(), "acme", "u-ada", now)
	if err != nil || r.Scope != usage.ScopeAccount || r.Fallback != "your own limit wasn't read (status 500)" {
		t.Errorf("failed read: %+v %v", r, err)
	}
	srv2 := nexusAPI(t, usersAda, `{"used":{"currencyCode":"USD","units":"3"},"effectiveLimit":null}`)
	defer srv2.Close()
	r, err = client(srv2).Read(context.Background(), "acme", "u-ada", now)
	if err != nil || r.Scope != usage.ScopeAccount || r.Fallback != usage.FallbackNoCap {
		t.Errorf("no cap: %+v %v", r, err)
	}
}

// A per-user 404 names the user and account the operator set; an account
// that is not the key's is named too, and the key's account is read.
func TestReadNamesAWrongUserOrAccount(t *testing.T) {
	srv := nexusAPI(t, usersAda, limitsBlocked)
	defer srv.Close()
	r, err := client(srv).Read(context.Background(), "acme", "u-42", now)
	if err != nil || r.Scope != usage.ScopeAccount || r.Fallback != "user u-42 not found in account acme" {
		t.Errorf("wrong user: %+v %v", r, err)
	}
	r, err = client(srv).Read(context.Background(), "other", "u-ada", now)
	if err != nil || r.AccountID != "acme" || r.Fallback != "account other is not your key's account (acme)" {
		t.Errorf("wrong account: %+v %v", r, err)
	}
}
