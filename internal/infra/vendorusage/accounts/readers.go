// Package accounts holds the HTTP readers for gateway and API vendors' own
// account figures (ADR 0009 §7): OpenRouter, DeepSeek, Moonshot, the
// subscription plans in plans.go and the gateways in gateways.go. Each
// satisfies a port of internal/contexts/spend/vendorusage/accounts, whose
// poller decides which key goes where. Each reader calls only its vendor's
// documented endpoint; keys are never stored or logged.
package accounts

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// Readers is every vendor with an account endpoint we read.
func Readers() []usage.Reader {
	return []usage.Reader{OpenRouter{}, DeepSeek{}, Moonshot{},
		ZAI{}, Kimi{}, MiniMax{}, Synthetic{}, Chutes{}, DeepInfra{}, Vercel{}}
}

// getJSON GETs url with a bearer key and decodes the body into out.
func getJSON(ctx context.Context, hc *http.Client, url, key string, out any) error {
	return getJSONAuth(ctx, hc, url, "Bearer "+key, out)
}

// getJSONAuth GETs url with the Authorization header set to auth.
func getJSONAuth(ctx context.Context, hc *http.Client, url, auth string, out any) error {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", auth)
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
		return fmt.Errorf("%w (%d on %s)", usage.ErrAuth, resp.StatusCode, hostPath(url))
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

func (o OpenRouter) Read(ctx context.Context, key string) (usage.Reading, error) {
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
		return usage.Reading{}, err
	}
	d := resp.Data
	r := usage.Reading{Scope: "key", UsedUSD: d.UsageMonthly, HasUsed: true}
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

func (d DeepSeek) Read(ctx context.Context, key string) (usage.Reading, error) {
	var resp struct {
		IsAvailable  bool `json:"is_available"`
		BalanceInfos []struct {
			Currency     string `json:"currency"`
			TotalBalance string `json:"total_balance"`
		} `json:"balance_infos"`
	}
	if err := getJSON(ctx, d.HTTP, base(d.BaseURL, "https://api.deepseek.com")+"/user/balance", key, &resp); err != nil {
		return usage.Reading{}, err
	}
	r := usage.Reading{Scope: "account", LimitReached: !resp.IsAvailable}
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

func (m Moonshot) Read(ctx context.Context, key string) (usage.Reading, error) {
	var resp struct {
		Data struct {
			AvailableBalance float64 `json:"available_balance"`
		} `json:"data"`
		Status bool `json:"status"`
	}
	if err := getJSON(ctx, m.HTTP, base(m.BaseURL, "https://api.moonshot.ai")+"/v1/users/me/balance", key, &resp); err != nil {
		return usage.Reading{}, err
	}
	b := resp.Data.AvailableBalance
	return usage.Reading{Scope: "account", BalanceUSD: b, HasBalance: true, LimitReached: b <= 0}, nil
}
