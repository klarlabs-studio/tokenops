package accounts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// readerHyper and readerHyperWeb register the readers (readers_gen.go).
func readerHyper() usage.Reader    { return Hyper{} }
func readerHyperWeb() usage.Reader { return HyperWeb{} }

// Hyper reads the Hypercredits left on Charm Hyper with an API key, from
// GET hyper.charm.land/v1/credits, as CodexBar's Charm Hyper provider does
// (Sources/CodexBarCore/Resources/Plugins/hyper.ts, docs/hyper.md). The
// balance is in Hypercredits and is kept in them.
type Hyper struct {
	BaseURL string
	HTTP    *http.Client
}

func (Hyper) Endpoint() string               { return "hyper" }
func (Hyper) Provider() eventschema.Provider { return "hyper" }
func (Hyper) Source() string                 { return "hyper-account" }
func (Hyper) KeyOnly() bool                  { return true }

func (h Hyper) Read(ctx context.Context, key string) (usage.Reading, error) {
	if isSession(key) {
		return usage.Reading{}, usage.ErrSkip
	}
	var raw json.RawMessage
	if err := getJSON(ctx, h.HTTP, base(h.BaseURL, "https://hyper.charm.land")+"/v1/credits", strings.TrimSpace(key), &raw); err != nil {
		return usage.Reading{}, err
	}
	return hyperReading(raw)
}

// HyperWeb reads the balance with the hyper.charm.land browser session.
// /v1/credits takes bearer keys only, so the session reads the signed-in
// team dashboard instead, as CodexBar does since 28ded89ecb
// (Sources/CodexBarCore/Resources/Plugins/hyper.ts): GET / lands on
// /teams/<uuid>/dashboard, following redirects on the same site only. A
// page naming no team is a signed-out session; the balance is the number
// in the "Hypercredits Available" card.
type HyperWeb struct {
	BaseURL string
	HTTP    *http.Client
}

func (HyperWeb) Endpoint() string               { return "hyper" }
func (HyperWeb) Provider() eventschema.Provider { return "hyper" }
func (HyperWeb) Source() string                 { return "hyper-web" }

var (
	hyperTeam    = lazyRegexp(`/teams/[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`)
	hyperBalance = lazyRegexp(`Hypercredits Available[\s\S]{0,400}?text-5xl font-bold">\s*([0-9][0-9,]*(?:\.[0-9]+)?)\s*<`)
)

// hyperRedirects bounds the same-site redirects followed from /.
const hyperRedirects = 3

func (h HyperWeb) Read(ctx context.Context, cookie string) (usage.Reading, error) {
	cookie = cookieHeader(cookie)
	if !isSession(cookie) {
		return usage.Reading{}, usage.ErrSkip
	}
	root := base(h.BaseURL, "https://hyper.charm.land")
	where, body, err := h.dashboard(ctx, root, cookie)
	if err != nil {
		return usage.Reading{}, err
	}
	if !hyperTeam().MatchString(where) && !hyperTeam().Match(body) {
		return usage.Reading{}, fmt.Errorf("%w (hyper.charm.land shows no team: the session is signed out)", usage.ErrAuth)
	}
	m := hyperBalance().FindSubmatch(body)
	if m == nil {
		return usage.Reading{}, errors.New("accounts: hyper.charm.land dashboard: no Hypercredits balance on the page")
	}
	v, err := strconv.ParseFloat(strings.ReplaceAll(string(m[1]), ",", ""), 64)
	if err != nil {
		return usage.Reading{}, errors.New("accounts: hyper.charm.land dashboard: unreadable balance")
	}
	return usage.Reading{Scope: "account", Credits: v, CreditsUnit: "Hypercredits", HasCredits: true}, nil
}

// dashboard GETs / with the session and follows redirects that stay on
// root's host, returning the page's path and body.
func (h HyperWeb) dashboard(ctx context.Context, root, cookie string) (string, []byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	site, err := url.Parse(root + "/")
	if err != nil {
		return "", nil, err
	}
	next := site
	for range hyperRedirects + 1 {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, next.String(), nil)
		if err != nil {
			return "", nil, err
		}
		req.Header.Set("Cookie", cookie)
		req.Header.Set("Accept", "text/html")
		req.Header.Set("User-Agent", browserUA)
		resp, err := noRedirect(h.HTTP).Do(req)
		if err != nil {
			return "", nil, fmt.Errorf("accounts: GET %s: %w", hostPath(next.String()), withoutQuery(err))
		}
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
		_ = resp.Body.Close()
		switch s := resp.StatusCode; {
		case s >= 300 && s < 400:
			loc, err := next.Parse(resp.Header.Get("Location"))
			if err != nil || loc.Host != site.Host || loc.Scheme != site.Scheme {
				return "", nil, fmt.Errorf("%w (redirected off hyper.charm.land)", usage.ErrAuth)
			}
			next = loc
			continue
		case s == http.StatusUnauthorized || s == http.StatusForbidden:
			return "", nil, fmt.Errorf("%w (%d on %s)", usage.ErrAuth, s, hostPath(next.String()))
		case s != http.StatusOK:
			return "", nil, &statusError{method: http.MethodGet, where: hostPath(next.String()), status: s}
		}
		return next.Path, body, nil
	}
	return "", nil, errors.New("accounts: hyper.charm.land: too many redirects")
}

// hyperReading is {"balance": n}, a finite number at or above zero.
func hyperReading(body []byte) (usage.Reading, error) {
	var resp struct {
		Balance json.RawMessage `json:"balance"`
	}
	if json.Unmarshal(body, &resp) != nil {
		return usage.Reading{}, errors.New("accounts: GET hyper.charm.land/v1/credits: unexpected shape")
	}
	v, ok := finiteNumber(resp.Balance)
	if !ok || v < 0 {
		return usage.Reading{}, errors.New("accounts: GET hyper.charm.land/v1/credits: unreadable balance")
	}
	return usage.Reading{Scope: "account", Credits: v, CreditsUnit: "Hypercredits", HasCredits: true}, nil
}
