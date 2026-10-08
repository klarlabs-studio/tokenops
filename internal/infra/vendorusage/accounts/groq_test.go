package accounts

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
)

func groqNow() time.Time { return time.Date(2026, 7, 14, 9, 0, 0, 0, time.UTC) }

// groqJWT is an unsigned JWT naming the organisation as the console's
// does.
func groqJWT(claims string) string {
	enc := base64.RawURLEncoding.EncodeToString
	return enc([]byte(`{"alg":"none"}`)) + "." + enc([]byte(claims)) + ".sig"
}

var groqToken = groqJWT(`{"https://groq.com/organization":{"id":"org_fixture"}}`)

// groqServer is Stytch and the GroqCloud API: Stytch exchanges the session
// "opaque" for groqToken; the API answers the activity for groqToken.
func groqServer(t *testing.T, activity string, queries *[]string) *httptest.Server {
	t.Helper()
	routes := routesOf(t, "groq")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/sdk/v1/b2b/sessions/authenticate":
			want := "Basic " + base64.StdEncoding.EncodeToString([]byte(groqStytchPublicToken+":opaque"))
			if r.Header.Get("Authorization") != want || r.Header.Get("X-Sdk-Parent-Host") != "https://console.groq.com" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			_, _ = w.Write([]byte(strings.Replace(routes[r.URL.Path], "JWT", groqToken, 1)))
		default:
			if r.Header.Get("Authorization") != "Bearer "+groqToken {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			if queries != nil {
				*queries = append(*queries, r.URL.RawQuery)
			}
			body, ok := routes[r.URL.Path]
			if activity != "" {
				body = activity
			}
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			_, _ = w.Write([]byte(body))
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestGroqReadsTheMonthsSpend(t *testing.T) {
	var queries []string
	srv := groqServer(t, "", &queries)
	got, err := Groq{BaseURL: srv.URL, StytchURL: srv.URL, Now: groqNow}.Read(context.Background(), "stytch_session=opaque")
	if err != nil || !got.HasUsed || !approx(got.UsedUSD, 0.035) || got.LimitUSD != 0 || got.Subscription {
		t.Fatalf("got %+v, %v", got, err)
	}
	// July 1 to the end of today, UTC.
	if len(queries) != 1 || !strings.Contains(queries[0], "start_date=1782864000") || !strings.Contains(queries[0], "end_date=1784073600") {
		t.Errorf("query %v", queries)
	}
}

// A session holding only the short-lived JWT is read with it; the Stytch
// slug names the organisation when the Groq claim is absent.
func TestGroqReadsWithTheJWTAlone(t *testing.T) {
	srv := groqServer(t, "", nil)
	got, err := Groq{BaseURL: srv.URL, StytchURL: srv.URL, Now: groqNow}.Read(context.Background(), "stytch_session_jwt="+groqToken)
	if err != nil || !approx(got.UsedUSD, 0.035) {
		t.Fatalf("got %+v, %v", got, err)
	}
	if org := groqOrganization(groqJWT(`{"https://stytch.com/organization":{"slug":"acme"}}`)); org != "acme" {
		t.Errorf("slug = %q", org)
	}
}

func TestGroqRefusals(t *testing.T) {
	srv := groqServer(t, "", nil)
	if _, err := (Groq{BaseURL: srv.URL, StytchURL: srv.URL}).Read(context.Background(), "stytch_session=revoked"); !errors.Is(err, usage.ErrAuth) {
		t.Errorf("revoked session = %v", err)
	}
	if _, err := (Groq{BaseURL: srv.URL, StytchURL: srv.URL}).Read(context.Background(), "stytch_session_jwt="+groqJWT(`{"sub":"x"}`)); !errors.Is(err, usage.ErrAuth) {
		t.Errorf("no organisation = %v", err)
	}
	if _, err := (Groq{BaseURL: srv.URL, StytchURL: srv.URL}).Read(context.Background(), "gsk_key"); !errors.Is(err, usage.ErrAuth) {
		t.Errorf("an API key = %v", err)
	}
}

func TestGroqUnknownShape(t *testing.T) {
	for _, body := range []string{`{"object":"list"}`, `{"data":[{"cost":1}]}`, `[]`} {
		srv := groqServer(t, body, nil)
		if got, err := (Groq{BaseURL: srv.URL, StytchURL: srv.URL, Now: groqNow}).Read(context.Background(), "stytch_session=opaque"); err == nil {
			t.Errorf("%s = %+v", body, got)
		}
	}
}
