package accounts

import (
	"context"
	"errors"
	"net/http"
	"time"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// readerDevPass registers the reader (readers_gen.go).
func readerDevPass() usage.Reader { return DevPass{} }

// DevPass reads LLM Gateway's GET /v1/key
// (https://docs.llmgateway.io/developers/devpass-usage), as CodexBar's
// DevPass provider does: on a DevPass plan, the billing cycle's plan
// credits and the premium-model weekly window, both organisation-wide; on
// pay-as-you-go (devPlan "none"), the key's all-time spend against its own
// limit. Amounts are decimal strings in dollars. The endpoint gives no
// date for the cycle's renewal, so none is invented.
type DevPass struct {
	BaseURL string
	HTTP    *http.Client
}

func (DevPass) Endpoint() string               { return "devpass" }
func (DevPass) Provider() eventschema.Provider { return "devpass" }
func (DevPass) Source() string                 { return "devpass-account" }

func (d DevPass) Read(ctx context.Context, key string) (usage.Reading, error) {
	var resp struct {
		Data *struct {
			Usage               number `json:"usage"`
			Limit               number `json:"limit"`
			DevPlan             string `json:"devPlan"`
			CreditsUsed         number `json:"devPlanCreditsUsed"`
			CreditsLimit        number `json:"devPlanCreditsLimit"`
			PremiumWeeklyLimit  number `json:"devPlanPremiumWeeklyLimit"`
			PremiumCreditsUsed  number `json:"devPlanPremiumCreditsUsed"`
			PremiumWeekResetsAt string `json:"devPlanPremiumWeekResetsAt"`
		} `json:"data"`
	}
	if err := getJSON(ctx, d.HTTP, base(d.BaseURL, "https://api.llmgateway.io")+"/v1/key", key, &resp); err != nil {
		return usage.Reading{}, err
	}
	p := resp.Data
	if p == nil {
		return usage.Reading{}, errors.New("accounts: GET api.llmgateway.io/v1/key: unrecognised answer")
	}
	switch p.DevPlan {
	case "none":
		r := usage.Reading{Scope: "key", UsedUSD: p.Usage.v, HasUsed: p.Usage.ok}
		if p.Limit.ok && p.Limit.v > 0 {
			r.LimitUSD = p.Limit.v
			r.LimitReached = p.Usage.v >= p.Limit.v
		}
		return r, nil
	case "lite", "pro", "max":
	default:
		return usage.Reading{}, errors.New("accounts: GET api.llmgateway.io/v1/key: unknown devPlan")
	}
	r := usage.Reading{Scope: "account", Subscription: true}
	// A zero allowance has no percentage to show.
	if p.CreditsLimit.ok && p.CreditsLimit.v > 0 {
		r.Windows = append(r.Windows, usage.Window{Name: "month", UsedPct: clampPct(pct(p.CreditsUsed.v, p.CreditsLimit.v))})
	}
	if p.PremiumWeeklyLimit.ok && p.PremiumWeeklyLimit.v > 0 {
		// The premium window opens with the first premium request; an
		// inactive one has no reset.
		r.Windows = append(r.Windows, usage.Window{Name: "premium week", UsedPct: clampPct(pct(p.PremiumCreditsUsed.v, p.PremiumWeeklyLimit.v)),
			Duration: 7 * 24 * time.Hour, ResetsAt: parseTime(p.PremiumWeekResetsAt)})
	}
	return r, nil
}
