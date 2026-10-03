// Package accounts reads gateway and API vendors' own account figures,
// with the key the operator's harness already sends them (ADR 0009 §7):
// a key's spend and cap on OpenRouter, the prepaid balance on DeepSeek and
// Moonshot. Each reader calls only its vendor's documented endpoint, with a
// key found for that vendor's endpoint; keys are never stored or logged.
package accounts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// ErrAuth reports a key the vendor refused.
var ErrAuth = errors.New("accounts: the key was refused")

// Credential is a key and the endpoint it was found for.
type Credential struct {
	// Endpoint is the endpoint name (biller's), e.g. "openrouter".
	Endpoint string
	// Origin says where it was found, for status; never the key.
	Origin string
	Key    string
}

// Reading is what a vendor reports about the account.
type Reading struct {
	// Scope names what the figures cover: "key", "account".
	Scope string
	// UsedUSD is spend in the period, when the vendor reports it.
	UsedUSD float64
	HasUsed bool
	// LimitUSD is the cap UsedUSD is spent against, 0 for none.
	LimitUSD float64
	// BalanceUSD is prepaid credit left, when the vendor reports it.
	BalanceUSD float64
	HasBalance bool
	// LimitReached is the vendor saying requests are blocked.
	LimitReached bool
}

// Reader reads one vendor's account.
type Reader interface {
	// Endpoint is the endpoint whose keys this reader uses.
	Endpoint() string
	// Provider is the biller the reading is for.
	Provider() eventschema.Provider
	// Source is the event source tag.
	Source() string
	Read(ctx context.Context, key string) (Reading, error)
}

// Readers is every vendor with a documented account endpoint.
func Readers() []Reader {
	return []Reader{OpenRouter{}, DeepSeek{}, Moonshot{}}
}

// getJSON GETs url with a bearer key and decodes the body into out.
func getJSON(ctx context.Context, hc *http.Client, url, key string, out any) error {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Accept", "application/json")
	if hc == nil {
		hc = &http.Client{Timeout: 20 * time.Second}
	}
	resp, err := hc.Do(req)
	if err != nil {
		return fmt.Errorf("accounts: GET %s: %w", hostPath(url), err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return fmt.Errorf("%w (%d on %s)", ErrAuth, resp.StatusCode, hostPath(url))
	case resp.StatusCode != http.StatusOK:
		return fmt.Errorf("accounts: GET %s: status %d", hostPath(url), resp.StatusCode)
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("accounts: GET %s: %w", hostPath(url), err)
	}
	return nil
}

func hostPath(url string) string {
	if i := strings.Index(url, "?"); i >= 0 {
		return url[:i]
	}
	return url
}

func base(override, def string) string {
	if override != "" {
		return strings.TrimRight(override, "/")
	}
	return def
}

// OpenRouter reads GET /api/v1/key: the key's usage and its credit cap
// (https://openrouter.ai/docs/api_reference/limits). The account's own
// credit balance needs a management key and is not read.
type OpenRouter struct {
	BaseURL string
	HTTP    *http.Client
}

func (OpenRouter) Endpoint() string               { return "openrouter" }
func (OpenRouter) Provider() eventschema.Provider { return eventschema.ProviderOpenRouter }
func (OpenRouter) Source() string                 { return "openrouter-account" }

func (o OpenRouter) Read(ctx context.Context, key string) (Reading, error) {
	var resp struct {
		Data struct {
			Limit          *float64 `json:"limit"`
			LimitReset     *string  `json:"limit_reset"`
			LimitRemaining *float64 `json:"limit_remaining"`
			Usage          float64  `json:"usage"`
			UsageDaily     float64  `json:"usage_daily"`
			UsageWeekly    float64  `json:"usage_weekly"`
			UsageMonthly   float64  `json:"usage_monthly"`
		} `json:"data"`
	}
	if err := getJSON(ctx, o.HTTP, base(o.BaseURL, "https://openrouter.ai")+"/api/v1/key", key, &resp); err != nil {
		return Reading{}, err
	}
	d := resp.Data
	r := Reading{Scope: "key", UsedUSD: d.UsageMonthly, HasUsed: true}
	if d.Limit == nil {
		return r, nil
	}
	// A cap is spent against the period it resets on.
	r.LimitUSD = *d.Limit
	reset := ""
	if d.LimitReset != nil {
		reset = strings.ToLower(*d.LimitReset)
	}
	switch reset {
	case "daily":
		r.UsedUSD = d.UsageDaily
	case "weekly":
		r.UsedUSD = d.UsageWeekly
	case "monthly":
		r.UsedUSD = d.UsageMonthly
	default:
		r.UsedUSD = d.Usage
	}
	if d.LimitRemaining != nil {
		r.LimitReached = *d.LimitRemaining <= 0
	}
	return r, nil
}

// DeepSeek reads GET /user/balance: the prepaid balance
// (https://api-docs.deepseek.com/api/get-user-balance). Only a USD
// balance is used; headroom is in dollars.
type DeepSeek struct {
	BaseURL string
	HTTP    *http.Client
}

func (DeepSeek) Endpoint() string               { return "deepseek" }
func (DeepSeek) Provider() eventschema.Provider { return eventschema.ProviderDeepSeek }
func (DeepSeek) Source() string                 { return "deepseek-account" }

func (d DeepSeek) Read(ctx context.Context, key string) (Reading, error) {
	var resp struct {
		IsAvailable  bool `json:"is_available"`
		BalanceInfos []struct {
			Currency     string `json:"currency"`
			TotalBalance string `json:"total_balance"`
		} `json:"balance_infos"`
	}
	if err := getJSON(ctx, d.HTTP, base(d.BaseURL, "https://api.deepseek.com")+"/user/balance", key, &resp); err != nil {
		return Reading{}, err
	}
	r := Reading{Scope: "account", LimitReached: !resp.IsAvailable}
	for _, b := range resp.BalanceInfos {
		if b.Currency != "USD" {
			continue
		}
		if v, err := strconv.ParseFloat(b.TotalBalance, 64); err == nil {
			r.BalanceUSD, r.HasBalance = v, true
		}
	}
	return r, nil
}

// Moonshot reads GET /v1/users/me/balance on the international platform
// (https://platform.kimi.ai/docs/api/balance), in USD.
type Moonshot struct {
	BaseURL string
	HTTP    *http.Client
}

func (Moonshot) Endpoint() string               { return "moonshot" }
func (Moonshot) Provider() eventschema.Provider { return "moonshot" }
func (Moonshot) Source() string                 { return "moonshot-account" }

func (m Moonshot) Read(ctx context.Context, key string) (Reading, error) {
	var resp struct {
		Data struct {
			AvailableBalance float64 `json:"available_balance"`
		} `json:"data"`
		Status bool `json:"status"`
	}
	if err := getJSON(ctx, m.HTTP, base(m.BaseURL, "https://api.moonshot.ai")+"/v1/users/me/balance", key, &resp); err != nil {
		return Reading{}, err
	}
	b := resp.Data.AvailableBalance
	return Reading{Scope: "account", BalanceUSD: b, HasBalance: true, LimitReached: b <= 0}, nil
}
