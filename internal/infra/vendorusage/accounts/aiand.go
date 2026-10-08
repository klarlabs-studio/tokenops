package accounts

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"time"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// readerAiAnd registers the reader (readers_gen.go).
func readerAiAnd() usage.Reader { return AiAnd{} }

// aiandPages bounds one reading: 100 log rows a page.
const aiandPages = 20

// AiAnd sums the organisation's spend over the last 30 days from GET
// /logs (https://docs.aiand.com/analytics/logs/), as CodexBar does: the
// documented analytics summary carries no cost, and the logs are the only
// endpoint that does. ai& keeps logs for 30 days, which is the window.
// Costs are decimal strings in the organisation's billing currency, summed
// exactly; an organisation billed in yen has no dollar figure to report.
// An answer of 402 is ai& saying the organisation is out of credit.
type AiAnd struct {
	BaseURL string
	HTTP    *http.Client
}

func (AiAnd) Endpoint() string               { return "aiand" }
func (AiAnd) Provider() eventschema.Provider { return "aiand" }
func (AiAnd) Source() string                 { return "aiand-account" }

func (a AiAnd) Read(ctx context.Context, key string) (usage.Reading, error) {
	root := base(a.BaseURL, "https://api.aiand.com") + "/logs?range=30days&limit=100"
	total, currency := new(big.Rat), ""
	after, afterID := "", ""
	for page := 0; page < aiandPages; page++ {
		u := root
		if after != "" && afterID != "" {
			u += "&after=" + url.QueryEscape(after) + "&after_id=" + url.QueryEscape(afterID)
		}
		var resp struct {
			Data []struct {
				Cost     *string `json:"cost"`
				Currency string  `json:"currency"`
			} `json:"data"`
			HasMore     bool    `json:"has_more"`
			NextAfter   *string `json:"next_after"`
			NextAfterID *string `json:"next_after_id"`
		}
		if err := getJSON(ctx, a.HTTP, u, key, &resp); err != nil {
			var se *statusError
			if errors.As(err, &se) && se.status == http.StatusPaymentRequired {
				return usage.Reading{Scope: "account", LimitReached: true}, nil
			}
			return usage.Reading{}, err
		}
		if resp.Data == nil {
			return usage.Reading{}, errors.New("accounts: aiand logs: unrecognised answer")
		}
		for _, row := range resp.Data {
			code := strings.ToUpper(strings.TrimSpace(row.Currency))
			if row.Cost == nil || code == "" {
				continue
			}
			cost, ok := new(big.Rat).SetString(strings.TrimSpace(*row.Cost))
			if !ok {
				return usage.Reading{}, errors.New("accounts: aiand logs: unreadable cost")
			}
			if currency == "" {
				currency = code
			}
			if code == currency {
				total.Add(total, cost)
			}
		}
		if !resp.HasMore {
			return aiandReading(total, currency)
		}
		if resp.NextAfter == nil || resp.NextAfterID == nil {
			break
		}
		after, afterID = *resp.NextAfter, *resp.NextAfterID
	}
	// A truncated sum would understate the spend; say so instead.
	return usage.Reading{}, fmt.Errorf("accounts: aiand logs: more than %d requests in 30 days; spend not summed", aiandPages*100)
}

func aiandReading(total *big.Rat, currency string) (usage.Reading, error) {
	switch currency {
	case "":
		// No billed request in 30 days: nothing says which currency.
		return usage.Reading{Scope: "account"}, nil
	case "USD":
		v, _ := total.Float64()
		return usage.Reading{Scope: "account", UsedUSD: v, HasUsed: true, UsedPeriod: 30 * 24 * time.Hour}, nil
	}
	return usage.Reading{}, fmt.Errorf("accounts: aiand bills this organisation in %s; TokenOps reads dollars only", currency)
}
