package accounts

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// readerTypeSafe registers the reader (readers_gen.go).
func readerTypeSafe() usage.Reader { return TypeSafe{} }

// TypeSafe reads the spend and credit balance TypeSafe's console billing
// page shows, ported from CodexBar
// (Sources/CodexBarCore/Resources/Plugins/typesafe.ts, Providers/TypeSafe,
// docs/typesafe.md), with the console session's Cookie header pasted at
// setup (its session cookie is not known by name; an inference API key
// does not read billing).
//
// The billing page moved to a JSON endpoint, GET /api/billing/overview
// (CodexBar b22cdc2fd7), which refuses a request that does not look
// same-origin (d1f1f2b3d0) and is behind Cloudflare, which wants a browser
// User-Agent (77e62a80b8). Spend is this billing cycle's; the balance is
// the credit left, both in dollars.
type TypeSafe struct {
	BaseURL string
	HTTP    *http.Client
}

func (TypeSafe) Endpoint() string               { return "typesafe" }
func (TypeSafe) Provider() eventschema.Provider { return "typesafe" }
func (TypeSafe) Source() string                 { return "typesafe-account" }

var (
	errTypeSafeShape = errors.New("accounts: typesafe billing overview: unrecognised answer")
	typeSafeLoginRe  = lazyRegexp(`(?i)(?:^|[/?#])(?:login|sign-?in|signin|auth|oauth|sso)(?:[/?#]|$)`)
	typeSafeSignIn   = lazyRegexp(`(?i)login|sign[-\s]?in|\(auth\)`)
)

func (t TypeSafe) Read(ctx context.Context, key string) (usage.Reading, error) {
	cookie := cookieHeader(key)
	if !strings.Contains(cookie, "=") {
		return usage.Reading{}, fmt.Errorf("%w (the TypeSafe credential is not a Cookie header)", usage.ErrAuth)
	}
	origin := base(t.BaseURL, "https://console.typesafe.ai")
	body, err := t.get(ctx, origin, cookie)
	if err != nil {
		return usage.Reading{}, err
	}
	// A signed-out console can answer its sign-in page instead of JSON.
	if trimmed := bytes.TrimSpace(body); bytes.HasPrefix(trimmed, []byte("<")) {
		if typeSafeSignIn().Match(trimmed) {
			return usage.Reading{}, fmt.Errorf("%w (TypeSafe answered with its sign-in page)", usage.ErrAuth)
		}
		return usage.Reading{}, errTypeSafeShape
	}
	var resp struct {
		Billing *struct {
			Spent   *float64 `json:"spent"`
			Balance *float64 `json:"balance"`
		} `json:"billing"`
	}
	if json.Unmarshal(body, &resp) != nil || resp.Billing == nil || resp.Billing.Spent == nil || resp.Billing.Balance == nil {
		return usage.Reading{}, errTypeSafeShape
	}
	return usage.Reading{Scope: "account", UsedUSD: *resp.Billing.Spent, HasUsed: true, BalanceUSD: *resp.Billing.Balance, HasBalance: true}, nil
}

// get asks for the overview without following a redirect: one to a sign-in
// or SSO route is a signed-out session, any other an endpoint that moved.
func (t TypeSafe) get(ctx context.Context, origin, cookie string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	where := origin + "/api/billing/overview"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, where, nil)
	if err != nil {
		return nil, err
	}
	for k, v := range map[string]string{
		"Cookie": cookie, "Accept": "application/json", "Origin": origin, "Referer": origin + "/billing",
		"Sec-Fetch-Site": "same-origin", "Sec-Fetch-Mode": "cors", "Sec-Fetch-Dest": "empty", "User-Agent": browserUA,
	} {
		req.Header.Set(k, v)
	}
	resp, err := noRedirect(t.HTTP).Do(req)
	if err != nil {
		return nil, fmt.Errorf("accounts: GET %s: %w", where, withoutQuery(err))
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	switch s := resp.StatusCode; {
	case s == http.StatusForbidden && bytes.Contains(bytes.ToLower(body), []byte("cross-origin request refused")):
		return nil, errors.New("accounts: typesafe billing overview: the request was refused as cross-origin")
	case s == http.StatusUnauthorized || s == http.StatusForbidden:
		return nil, fmt.Errorf("%w (%d on %s)", usage.ErrAuth, s, where)
	case s >= 300 && s < 400:
		if typeSafeLoginRedirect(resp.Header.Get("Location")) {
			return nil, fmt.Errorf("%w (redirected to sign in from %s)", usage.ErrAuth, where)
		}
		return nil, &statusError{method: http.MethodGet, where: where, status: s}
	case s < 200 || s >= 300:
		return nil, &statusError{method: http.MethodGet, where: where, status: s}
	}
	return body, nil
}

// typeSafeLoginRedirect reports whether a redirect goes to a sign-in: a
// login, auth or SSO route, or any host outside typesafe.ai.
func typeSafeLoginRedirect(location string) bool {
	location = strings.TrimSpace(location)
	if location == "" {
		return false
	}
	u, err := url.Parse(location)
	if err != nil {
		return true
	}
	if h := strings.ToLower(u.Hostname()); h != "" && h != "typesafe.ai" && !strings.HasSuffix(h, ".typesafe.ai") {
		return true
	}
	return typeSafeLoginRe().MatchString(u.Path) || typeSafeLoginRe().MatchString(location)
}
