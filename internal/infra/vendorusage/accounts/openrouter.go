package accounts

import (
	"context"
	"errors"
	"net/http"
	"strings"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// readerOpenRouter registers the reader (readers_gen.go).
func readerOpenRouter() usage.Reader { return OpenRouter{} }

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
			Usage          number   `json:"usage"`
			UsageDaily     number   `json:"usage_daily"`
			UsageWeekly    number   `json:"usage_weekly"`
			UsageMonthly   number   `json:"usage_monthly"`
		} `json:"data"`
	}
	if err := getJSON(ctx, o.HTTP, base(o.BaseURL, "https://openrouter.ai")+"/api/v1/key", key, &resp); err != nil {
		return usage.Reading{}, err
	}
	d := resp.Data
	r := usage.Reading{Scope: "key"}
	used := d.UsageMonthly
	if d.Limit != nil {
		// A cap is spent against the period it resets on.
		r.LimitUSD = *d.Limit
		reset := ""
		if d.LimitReset != nil {
			reset = strings.ToLower(*d.LimitReset)
		}
		switch reset {
		case "daily":
			used = d.UsageDaily
		case "weekly":
			used = d.UsageWeekly
		case "monthly":
		default:
			used = d.Usage
		}
		if d.LimitRemaining != nil {
			r.LimitReached = *d.LimitRemaining <= 0
		}
	}
	// An answer without the period's usage is not a key that spent nothing.
	if !used.ok {
		return usage.Reading{}, errors.New("accounts: openrouter key: no usage in the answer")
	}
	r.UsedUSD, r.HasUsed = used.v, true
	return r, nil
}
