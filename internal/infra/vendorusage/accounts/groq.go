package accounts

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// readerGroq registers the reader (readers_gen.go).
func readerGroq() usage.Reader { return Groq{} }

// Groq reads the organisation's GroqCloud spend this month from the
// console's own activity call with the console's browser session, as
// CodexBar's Groq provider does
// (Sources/CodexBarCore/Providers/Groq/GroqConsoleSession.swift,
// GroqConsoleStytch.swift, GroqConsoleFetcher.swift, docs/groq.md). The
// console's long-lived Stytch session is exchanged for a short-lived JWT
// with the console's public Stytch token, as the console's own page does;
// a session holding only the JWT is read with it while it lasts. The
// organisation is the JWT's claim.
type Groq struct {
	BaseURL, StytchURL string
	HTTP               *http.Client
	Now                func() time.Time
}

func (Groq) Endpoint() string               { return "groq" }
func (Groq) Provider() eventschema.Provider { return "groq" }
func (Groq) Source() string                 { return "groq-web" }

// groqStytchPublicToken is the console's public Stytch token: public, it
// is in the console's page.
const groqStytchPublicToken = "public-token-live-58df57a9-a1f5-4066-bc0c-2ff942db684f"

func (g Groq) Read(ctx context.Context, cookie string) (usage.Reading, error) {
	cookie = cookieHeader(cookie)
	opaque, direct := cookieValue(cookie, "stytch_session"), cookieValue(cookie, "stytch_session_jwt")
	if !isSession(cookie) || (opaque == "" && direct == "") {
		return usage.Reading{}, fmt.Errorf("%w (GroqCloud is read with the console's stytch_session cookie)", usage.ErrAuth)
	}
	jwt := direct
	if opaque != "" {
		fresh, err := g.refresh(ctx, opaque)
		switch {
		case err == nil:
			jwt = fresh
		case direct == "":
			return usage.Reading{}, err
		}
	}
	org := groqOrganization(jwt)
	if org == "" {
		return usage.Reading{}, fmt.Errorf("%w (the console session names no organisation)", usage.ErrAuth)
	}
	now := time.Now
	if g.Now != nil {
		now = g.Now
	}
	t := now().UTC()
	start := time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(t.Year(), t.Month(), t.Day()+1, 0, 0, 0, 0, time.UTC)
	q := url.Values{"start_date": {strconv.FormatInt(start.Unix(), 10)}, "end_date": {strconv.FormatInt(end.Unix(), 10)}}
	var resp struct {
		Data *[]struct {
			Timestamp *float64 `json:"timestamp"`
			Cost      *float64 `json:"cost"`
		} `json:"data"`
	}
	u := base(g.BaseURL, "https://api.groq.com") + "/platform/v1/organizations/" + url.PathEscape(org) + "/activity?" + q.Encode()
	if err := getJSON(ctx, g.HTTP, u, jwt, &resp); err != nil {
		return usage.Reading{}, err
	}
	if resp.Data == nil {
		return usage.Reading{}, errors.New("accounts: GET api.groq.com activity: no data")
	}
	spent := 0.0
	for _, row := range *resp.Data {
		if row.Timestamp == nil {
			return usage.Reading{}, errors.New("accounts: GET api.groq.com activity: a row without a time")
		}
		if row.Cost != nil {
			spent += *row.Cost
		}
	}
	return usage.Reading{Scope: "team", UsedUSD: spent, HasUsed: true}, nil
}

// refresh exchanges the console's Stytch session for a JWT.
func (g Groq) refresh(ctx context.Context, opaque string) (string, error) {
	client := base64.StdEncoding.EncodeToString([]byte(`{"app":{"identifier":"console.groq.com"},"sdk":{"identifier":"Stytch.js Javascript SDK","version":"5.43.0"}}`))
	header := http.Header{
		"Authorization":     {"Basic " + base64.StdEncoding.EncodeToString([]byte(groqStytchPublicToken+":"+opaque))},
		"Origin":            {"https://console.groq.com"},
		"X-Sdk-Parent-Host": {"https://console.groq.com"},
		"X-Sdk-Client":      {client},
	}
	var resp struct {
		Data struct {
			JWT string `json:"session_jwt"`
		} `json:"data"`
	}
	u := base(g.StytchURL, "https://api.stytchb2b.groq.com") + "/sdk/v1/b2b/sessions/authenticate"
	if err := postJSON(ctx, g.HTTP, u, header, map[string]any{"session_token": opaque, "session_duration_minutes": 30}, &resp); err != nil {
		return "", err
	}
	if strings.TrimSpace(resp.Data.JWT) == "" {
		return "", errors.New("accounts: POST stytch sessions/authenticate: no session_jwt")
	}
	return strings.TrimSpace(resp.Data.JWT), nil
}

// groqOrganization is the organisation the JWT names (its signature is
// the API's to check, not ours).
func groqOrganization(jwt string) string {
	parts := strings.Split(jwt, ".")
	if len(parts) < 2 {
		return ""
	}
	payload, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(parts[1], "="))
	if err != nil {
		return ""
	}
	var claims struct {
		Groq *struct {
			ID string `json:"id"`
		} `json:"https://groq.com/organization"`
		Stytch *struct {
			Slug string `json:"slug"`
		} `json:"https://stytch.com/organization"`
	}
	if json.Unmarshal(payload, &claims) != nil {
		return ""
	}
	org := ""
	if claims.Groq != nil && claims.Groq.ID != "" {
		org = claims.Groq.ID
	} else if claims.Stytch != nil {
		org = claims.Stytch.Slug
	}
	if org == "." || org == ".." || strings.ContainsAny(org, "/?#") {
		return ""
	}
	return org
}
