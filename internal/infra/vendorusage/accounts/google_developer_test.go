package accounts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
)

// fixtureBenefits is the Ro0AYc result of testdata/google-developer.json:
// another benefit and an applied credit, from CodexBar's
// GoogleDeveloperPluginTests.
func fixtureBenefits(t *testing.T) *string {
	t.Helper()
	s := strings.TrimSpace(fixture(t, "google-developer"))
	return &s
}

// The benefits CodexBar's GoogleDeveloperPluginTests use.
const (
	googleOtherBenefit    = `[3,1,null,null,null,null,null,null,[2]]`
	googleAppliedCredit   = `[19,2,null,null,null,null,null,null,[1],[null,[1825588800,0],0,2,"",null,[["USD",100],["USD",87,500000000],"billingAccounts/000000-AAAAAA-BBBBBB",5,1],"00000000-0000-0000-0000-000000000000","00000000-0000-0000-0000-000000000000",null,null,null,null,null,null,22,[1794052800,0]],false,null,["$100.00 monthly Gen AI & Cloud credits","Start building.",null,"Manage credits","Apply"],[13,21,22,26],null,true,"Remaining balance: $87.50","Expiring on: Nov 7, 2027"]`
	googleUnappliedCredit = `[19,2,null,null,null,null,null,null,[1],[null,null,0,2]]`
)

// googleAccount is one signed-in account behind authuser=N; an empty
// email is a signed-out page, a nil benefits an RPC error.
type googleAccount struct {
	email    string
	benefits *string
}

func googleBenefits(entries ...string) *string {
	s := "[[" + strings.Join(entries, ",") + "]]"
	return &s
}

// googleEnvelope is batchexecute's chunked answer around one result.
func googleEnvelope(t *testing.T, benefits *string) string {
	t.Helper()
	payload := "null"
	if benefits != nil {
		b, _ := json.Marshal(*benefits)
		payload = string(b)
	}
	chunks := []string{
		`[["di",42],["af.httprm",41,"-1234567890",7]]`,
		`[["wrb.fr","Ro0AYc",` + payload + `,null,null,null,"generic"]]`,
		`[["e",4,null,null,1234]]`,
	}
	out := ")]}'\n\n"
	for _, c := range chunks {
		out += fmt.Sprintf("%d\n%s\n", len(c), c)
	}
	return out
}

func googleDeveloperServer(t *testing.T, accounts []googleAccount, requests *[]string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Cookie") != "SID=fixture; HSID=x" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		n, _ := strconv.Atoi(r.URL.Query().Get("authuser"))
		*requests = append(*requests, r.Method+" "+strconv.Itoa(n))
		if n >= len(accounts) {
			w.Header().Set("Location", "https://accounts.google.com/")
			w.WriteHeader(http.StatusFound)
			return
		}
		a := accounts[n]
		if r.Method == http.MethodGet {
			if r.URL.Path != "/benefits" {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			vals := `"cfb2h":"boq_fixture"`
			if a.email != "" {
				vals += `,"SNlM0e":"fixture-at-token:+/=","oPEP7c":"` + a.email + `"`
			}
			_, _ = w.Write([]byte("<html><script>window.WIZ_global_data = {" + vals + "};</script></html>"))
			return
		}
		_ = r.ParseForm()
		if r.URL.Path != "/_/GoogleDeveloperProfile/data/batchexecute" || r.URL.Query().Get("rpcids") != "Ro0AYc" ||
			r.PostForm.Get("at") != "fixture-at-token:+/=" || !strings.Contains(r.PostForm.Get("f.req"), "Ro0AYc") ||
			r.Header.Get("Origin") == "" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		_, _ = w.Write([]byte(googleEnvelope(t, a.benefits)))
	}))
}

func TestGoogleDeveloperReadsTheAppliedMonthlyCredit(t *testing.T) {
	var reqs []string
	srv := googleDeveloperServer(t, []googleAccount{{"someone@example.com", fixtureBenefits(t)}}, &reqs)
	defer srv.Close()
	got, err := GoogleDeveloper{BaseURL: srv.URL}.Read(context.Background(), "Cookie: SID=fixture; HSID=x")
	if err != nil || !got.HasBalance || !approx(got.BalanceUSD, 87.5) || got.Scope != "account" || len(reqs) != 2 {
		t.Fatalf("%+v %v %v", got, err, reqs)
	}
}

// The first applied credit wins after accounts without one, with an RPC
// error, or with the credit not applied yet.
func TestGoogleDeveloperWalksTheSignedInAccounts(t *testing.T) {
	var reqs []string
	srv := googleDeveloperServer(t, []googleAccount{
		{"a@example.com", googleBenefits(googleOtherBenefit)},
		{"b@example.com", nil},
		{"c@example.com", googleBenefits(googleUnappliedCredit)},
		{"d@example.com", googleBenefits(strings.Replace(googleAppliedCredit, `["USD",87,500000000]`, `["USD"]`, 1))},
	}, &reqs)
	defer srv.Close()
	got, err := GoogleDeveloper{BaseURL: srv.URL}.Read(context.Background(), "SID=fixture; HSID=x")
	if err != nil || !got.HasBalance || got.BalanceUSD != 0 || len(reqs) != 8 {
		t.Fatalf("%+v %v %v", got, err, reqs)
	}
}

func TestGoogleDeveloperNoCredit(t *testing.T) {
	for name, tc := range map[string]struct {
		accounts []googleAccount
		auth     bool
		want     string
	}{
		"signed out":  {[]googleAccount{{"", nil}}, true, ""},
		"unapplied":   {[]googleAccount{{"a@example.com", googleBenefits(googleUnappliedCredit)}}, false, "not applied"},
		"no credit":   {[]googleAccount{{"a@example.com", googleBenefits(googleOtherBenefit)}}, false, "no signed-in account"},
		"same email":  {[]googleAccount{{"a@example.com", nil}, {"a@example.com", googleBenefits(googleAppliedCredit)}}, false, "no signed-in account"},
		"mixed money": {[]googleAccount{{"a@example.com", googleBenefits(strings.Replace(googleAppliedCredit, `["USD",87,`, `["EUR",87,`, 1))}}, false, "unrecognised"},
	} {
		var reqs []string
		srv := googleDeveloperServer(t, tc.accounts, &reqs)
		_, err := GoogleDeveloper{BaseURL: srv.URL}.Read(context.Background(), "SID=fixture; HSID=x")
		srv.Close()
		if err == nil || errors.Is(err, usage.ErrAuth) != tc.auth || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := (GoogleDeveloper{}).Read(context.Background(), "AIza-key"); !errors.Is(err, usage.ErrAuth) {
		t.Errorf("a key: %v", err)
	}
}

// A credit in another currency is kept in it.
func TestGoogleDeveloperOtherCurrency(t *testing.T) {
	var reqs []string
	credit := strings.ReplaceAll(googleAppliedCredit, `"USD"`, `"EUR"`)
	srv := googleDeveloperServer(t, []googleAccount{{"a@example.com", googleBenefits(credit)}}, &reqs)
	defer srv.Close()
	got, err := GoogleDeveloper{BaseURL: srv.URL}.Read(context.Background(), "SID=fixture; HSID=x")
	if err != nil || !got.HasCredits || got.CreditsUnit != "EUR" || !approx(got.Credits, 87.5) || got.HasBalance {
		t.Fatalf("%+v %v", got, err)
	}
}
