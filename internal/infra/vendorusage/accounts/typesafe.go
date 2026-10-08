package accounts

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"sync"
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
// The page's data comes from a Next.js server action, whose ID changes
// with each TypeSafe deploy: it is found in the page's own script chunks,
// kept for 12 hours, and found again when TypeSafe says it is stale. The
// action is read-only (getBillingOverview). Spend is this billing cycle's;
// the balance is the credit left, both in dollars.
type TypeSafe struct {
	BaseURL string
	HTTP    *http.Client
}

func (TypeSafe) Endpoint() string               { return "typesafe" }
func (TypeSafe) Provider() eventschema.Provider { return "typesafe" }
func (TypeSafe) Source() string                 { return "typesafe-account" }

const typeSafeActionTTL = 12 * time.Hour

// typeSafeActions caches the discovered action ID per console origin.
var typeSafeActions = struct {
	sync.Mutex
	id map[string]typeSafeAction
}{id: map[string]typeSafeAction{}}

type typeSafeAction struct {
	id    string
	found time.Time
}

var (
	typeSafeScript   = regexp.MustCompile(`(?i)<script\b[^>]*\bsrc=["']([^"']+)["'][^>]*>`)
	typeSafeActionID = regexp.MustCompile(`(?i)"([0-9a-f]{40,})"[^)]{0,150}"getBillingOverviewResult"`)
	typeSafeLogin    = regexp.MustCompile(`\\?"\(auth\)\\?",\{\\?"children\\?":\[\\?"login\\?"`)
)

func (t TypeSafe) Read(ctx context.Context, key string) (usage.Reading, error) {
	cookie := cookieHeader(key)
	if !strings.Contains(cookie, "=") {
		return usage.Reading{}, fmt.Errorf("%w (the TypeSafe credential is not a Cookie header)", usage.ErrAuth)
	}
	origin := base(t.BaseURL, "https://console.typesafe.ai")
	action, err := t.action(ctx, origin, cookie, false)
	if err != nil {
		return usage.Reading{}, err
	}
	body, stale, err := t.post(ctx, origin, cookie, action)
	if err == nil && stale {
		if action, err = t.action(ctx, origin, cookie, true); err == nil {
			body, _, err = t.post(ctx, origin, cookie, action)
		}
	}
	if err != nil {
		return usage.Reading{}, err
	}
	if typeSafeLogin.Match(body) {
		return usage.Reading{}, fmt.Errorf("%w (TypeSafe answered with its sign-in page)", usage.ErrAuth)
	}
	billing, err := typeSafeBilling(body)
	if err != nil {
		return usage.Reading{}, err
	}
	return usage.Reading{Scope: "account", UsedUSD: billing.Spent.v, HasUsed: true, BalanceUSD: billing.Balance.v, HasBalance: true}, nil
}

// action is the billing overview's server-action ID, from the cache unless
// fresh is set or it is older than typeSafeActionTTL.
func (t TypeSafe) action(ctx context.Context, origin, cookie string, fresh bool) (string, error) {
	typeSafeActions.Lock()
	cached, ok := typeSafeActions.id[origin]
	typeSafeActions.Unlock()
	if ok && !fresh && time.Since(cached.found) < typeSafeActionTTL {
		return cached.id, nil
	}
	page, err := doWeb(ctx, t.HTTP, http.MethodGet, origin+"/settings/billing", http.Header{
		"Cookie": {cookie}, "Accept": {"text/html"}, "User-Agent": {browserUA},
	}, nil)
	if err != nil {
		return "", err
	}
	if typeSafeLogin.Match(page) {
		return "", fmt.Errorf("%w (TypeSafe answered with its sign-in page)", usage.ErrAuth)
	}
	var chunks []string
	for _, m := range typeSafeScript.FindAllSubmatch(page, -1) {
		src := string(m[1])
		if strings.HasPrefix(src, "/") {
			src = origin + src
		}
		path := src
		if i := strings.IndexAny(path, "?#"); i >= 0 {
			path = path[:i]
		}
		if !strings.HasPrefix(src, origin+"/") || !strings.HasSuffix(strings.ToLower(path), ".js") || len(chunks) >= 60 {
			continue
		}
		chunks = append(chunks, src)
	}
	for _, src := range chunks {
		// Static chunks are public: they are fetched without the session.
		js, err := doWeb(ctx, t.HTTP, http.MethodGet, src, http.Header{"User-Agent": {browserUA}}, nil)
		if err != nil {
			continue
		}
		if m := typeSafeActionID.FindSubmatch(js); m != nil {
			id := string(m[1])
			typeSafeActions.Lock()
			typeSafeActions.id[origin] = typeSafeAction{id: id, found: time.Now()}
			typeSafeActions.Unlock()
			return id, nil
		}
	}
	return "", errors.New("accounts: typesafe billing page: action not found")
}

// post calls the billing action. stale is TypeSafe saying the action ID is
// no longer deployed.
func (t TypeSafe) post(ctx context.Context, origin, cookie, action string) ([]byte, bool, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	url := origin + "/settings/billing"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader("[]"))
	if err != nil {
		return nil, false, err
	}
	req.Header.Set("Cookie", cookie)
	req.Header.Set("Origin", origin)
	req.Header.Set("Next-Action", action)
	req.Header.Set("Accept", "text/x-component")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", browserUA)
	client := http.Client{Timeout: 20 * time.Second}
	if t.HTTP != nil {
		client = *t.HTTP
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := client.Do(req)
	if err != nil {
		return nil, false, fmt.Errorf("accounts: POST %s: %w", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	switch {
	case resp.StatusCode == http.StatusNotFound && resp.Header.Get("X-Nextjs-Action-Not-Found") == "1":
		return nil, true, nil
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden,
		resp.StatusCode >= 300 && resp.StatusCode < 400:
		return nil, false, fmt.Errorf("%w (%d on %s)", usage.ErrAuth, resp.StatusCode, url)
	case resp.StatusCode < 200 || resp.StatusCode >= 300:
		return nil, false, &statusError{method: http.MethodPost, where: url, status: resp.StatusCode}
	}
	return body, false, nil
}

type typeSafeBillingFields struct {
	Spent   number `json:"spent"`
	Balance number `json:"balance"`
}

// typeSafeBilling finds the action's result in the React Server Components
// answer: the first "<id>:{...}" line whose object has "ok".
func typeSafeBilling(body []byte) (typeSafeBillingFields, error) {
	sc := bufio.NewScanner(bytes.NewReader(body))
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for sc.Scan() {
		line := sc.Bytes()
		i := bytes.IndexByte(line, ':')
		if i < 1 {
			continue
		}
		var result struct {
			OK   *bool `json:"ok"`
			Data *struct {
				Billing *typeSafeBillingFields `json:"billing"`
			} `json:"data"`
		}
		if json.Unmarshal(line[i+1:], &result) != nil || result.OK == nil {
			continue
		}
		if !*result.OK {
			return typeSafeBillingFields{}, errors.New("accounts: typesafe billing: the action failed")
		}
		if result.Data == nil || result.Data.Billing == nil || !result.Data.Billing.Spent.ok || !result.Data.Billing.Balance.ok {
			break
		}
		return *result.Data.Billing, nil
	}
	return typeSafeBillingFields{}, errors.New("accounts: typesafe billing: unrecognised answer")
}
