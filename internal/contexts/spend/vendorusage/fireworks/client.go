// Package fireworks reads a Fireworks account's own figures: spend this
// month and the limit it is spent against (ADR 0009 §7). FireConnect points
// Claude Code, Codex and opencode at Fireworks, and the open models it
// routes to bill there, so Fireworks is a biller with limits of its own.
//
// Only Fireworks' documented REST API is used
// (https://docs.fireworks.ai/api-reference, /nexus/usage-limits):
//
//   - GET /verifyApiKey names the key's account (x-fireworks-account-id).
//   - GET /v1/accounts/{a}/users/{u}/usageLimits is a member's own spend
//     and effective cap, on company (Nexus) accounts.
//   - GET /v1/accounts/{a}/billing/summary and the monthly-spend-usd quota
//     are an account's month spend and limit, for an account of one's own.
//
// The key is fetched for each call and never kept or logged.
package fireworks

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
)

// DefaultBaseURL is Fireworks' API.
const DefaultBaseURL = "https://api.fireworks.ai"

// SpendQuotaName is the account's monthly spend limit, in US dollars
// (`firectl quota update monthly-spend-usd`).
const SpendQuotaName = "monthly-spend-usd"

var (
	// ErrAuth reports a key Fireworks refused.
	ErrAuth = errors.New("fireworks: the API key was refused")
	// ErrNotFound reports a resource the account does not have.
	ErrNotFound = errors.New("fireworks: not found")
)

// Client calls the Fireworks API.
type Client struct {
	BaseURL string
	HTTP    *http.Client
	// Key returns the API key for one call. It is asked each time and
	// never stored, so a rotated key is picked up.
	Key func(ctx context.Context) (string, error)
}

// Scope says whose figures a Reading holds.
type Scope string

const (
	// ScopeUser is a member's own spend against their cap.
	ScopeUser Scope = "user"
	// ScopeAccount is the whole account's spend against its limit.
	ScopeAccount Scope = "account"
)

// Reading is the month's spend and limit, as Fireworks reports them.
type Reading struct {
	Scope     Scope
	AccountID string
	// UsedUSD is spend this billing period.
	UsedUSD float64
	// LimitUSD is the cap it is spent against, 0 when there is none.
	LimitUSD float64
	// LimitReached is Fireworks saying requests are blocked by the cap.
	LimitReached bool
}

// do runs one GET and decodes the JSON body into out; hdr, when set,
// receives the response headers.
func (c *Client) do(ctx context.Context, path string, q url.Values, out any, hdr *http.Header) error {
	key, err := c.Key(ctx)
	if err != nil {
		return err
	}
	base := c.BaseURL
	if base == "" {
		base = DefaultBaseURL
	}
	u := strings.TrimRight(base, "/") + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Accept", "application/json")
	hc := c.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 20 * time.Second}
	}
	resp, err := hc.Do(req)
	if err != nil {
		return fmt.Errorf("fireworks: GET %s: %w", path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return fmt.Errorf("%w (%d on %s)", ErrAuth, resp.StatusCode, path)
	case resp.StatusCode == http.StatusNotFound:
		return fmt.Errorf("%w: %s", ErrNotFound, path)
	case resp.StatusCode != http.StatusOK:
		return fmt.Errorf("fireworks: GET %s: status %d", path, resp.StatusCode)
	}
	if hdr != nil {
		*hdr = resp.Header
	}
	if out == nil || len(body) == 0 {
		return nil
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("fireworks: GET %s: %w", path, err)
	}
	return nil
}

// AccountID is the account the key belongs to.
func (c *Client) AccountID(ctx context.Context) (string, error) {
	var h http.Header
	if err := c.do(ctx, "/verifyApiKey", nil, nil, &h); err != nil {
		return "", err
	}
	id := strings.TrimPrefix(h.Get("x-fireworks-account-id"), "accounts/")
	if id == "" {
		return "", errors.New("fireworks: the key's account was not named")
	}
	return id, nil
}

// UserLimit reads one member's own spend and effective cap. A member may
// read their own; ok is false when the user has no cap.
func (c *Client) UserLimit(ctx context.Context, account, user string) (Reading, bool, error) {
	var raw map[string]json.RawMessage
	path := "/v1/accounts/" + url.PathEscape(account) + "/users/" + url.PathEscape(user) + "/usageLimits"
	if err := c.do(ctx, path, nil, &raw, nil); err != nil {
		return Reading{}, false, err
	}
	limit, hasLimit := amount(raw, "effectiveLimit", "effective_limit")
	if !hasLimit {
		return Reading{}, false, nil
	}
	used, _ := amount(raw, "used", "usage")
	_, blocked := raw["exceededUntil"]
	if !blocked {
		_, blocked = raw["exceeded_until"]
	}
	return Reading{Scope: ScopeUser, AccountID: account, UsedUSD: used, LimitUSD: limit, LimitReached: blocked}, true, nil
}

// AccountMonth reads the account's spend this month (UTC) and its monthly
// spend limit, 0 when it has none.
func (c *Client) AccountMonth(ctx context.Context, account string, now time.Time) (Reading, error) {
	now = now.UTC()
	start := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	// endTime is exclusive and day-granular, so tomorrow includes today.
	end := time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, time.UTC)
	q := url.Values{"startTime": {start.Format(time.RFC3339)}, "endTime": {end.Format(time.RFC3339)}}
	var summary struct {
		LineItems []struct {
			TotalCost Money `json:"totalCost"`
		} `json:"lineItems"`
	}
	base := "/v1/accounts/" + url.PathEscape(account)
	if err := c.do(ctx, base+"/billing/summary", q, &summary, nil); err != nil {
		return Reading{}, err
	}
	r := Reading{Scope: ScopeAccount, AccountID: account}
	for _, li := range summary.LineItems {
		r.UsedUSD += li.TotalCost.Value()
	}
	var quotas struct {
		Quotas []struct {
			Name  string `json:"name"`
			Value string `json:"value"`
		} `json:"quotas"`
	}
	if err := c.do(ctx, base+"/quotas", url.Values{"pageSize": {"200"}}, &quotas, nil); err == nil {
		for _, qt := range quotas.Quotas {
			if qt.Name == SpendQuotaName || strings.HasSuffix(qt.Name, "/"+SpendQuotaName) {
				r.LimitUSD, _ = strconv.ParseFloat(qt.Value, 64)
			}
		}
	}
	var acct struct {
		SuspendState string `json:"suspendState"`
	}
	if err := c.do(ctx, base, nil, &acct, nil); err == nil {
		r.LimitReached = acct.SuspendState == "MONTHLY_SPEND_LIMIT_EXCEEDED"
	}
	return r, nil
}

// Money is Fireworks' amount: whole units and nanos.
type Money struct {
	CurrencyCode string `json:"currencyCode"`
	Units        string `json:"units"`
	Nanos        int64  `json:"nanos"`
}

// Value is the amount as a float.
func (m Money) Value() float64 {
	u, _ := strconv.ParseFloat(m.Units, 64)
	return u + float64(m.Nanos)/1e9
}

// amount reads the first of keys present in raw as dollars: a Money, a
// number or a numeric string. ok is false when none is present.
func amount(raw map[string]json.RawMessage, keys ...string) (float64, bool) {
	for _, k := range keys {
		v, present := raw[k]
		if !present {
			continue
		}
		var m Money
		if json.Unmarshal(v, &m) == nil && (m.Units != "" || m.Nanos != 0 || m.CurrencyCode != "") {
			return m.Value(), true
		}
		var f float64
		if json.Unmarshal(v, &f) == nil {
			return f, true
		}
		var s string
		if json.Unmarshal(v, &s) == nil {
			if f, err := strconv.ParseFloat(s, 64); err == nil {
				return f, true
			}
		}
	}
	return 0, false
}

// Read is the best reading for this operator: their own cap on a company
// account, else the account's month. account and user may be empty; the
// account is then looked up from the key.
func (c *Client) Read(ctx context.Context, account, user string, now time.Time) (Reading, error) {
	if account == "" {
		id, err := c.AccountID(ctx)
		if err != nil {
			return Reading{}, err
		}
		account = id
	}
	if user != "" {
		// No cap, or limits not enabled for the account: the account's
		// own figures are the next best.
		if r, ok, err := c.UserLimit(ctx, account, user); err == nil && ok {
			return r, nil
		}
	}
	return c.AccountMonth(ctx, account, now)
}
