package copilot

import "context"

// UserSource fetches the operator's Copilot user record. The HTTP client
// in internal/infra/vendorusage/copilot satisfies it.
type UserSource interface {
	User(ctx context.Context) (*UserResponse, error)
}

// UserResponse mirrors the (undocumented but stable since 2022)
// `GET api.github.com/copilot_internal/user` shape. We capture the
// fields the IDE plugins read: quota snapshots for chat and
// premium-interaction buckets, plus a freshness timestamp.
//
// Since GitHub moved Copilot to usage-based billing (2026-06-01), the
// record also says whether the seat is billed in GitHub AI Credits
// (token_based_billing) and, per bucket, how many credits were used this
// cycle (credits_used). These two fields are seen in third-party reports
// of the response (openusage issue #1094, an org seat), not in any GitHub
// documentation.
type UserResponse struct {
	Login             string                   `json:"login"`
	ChatEnabled       bool                     `json:"chat_enabled"`
	CopilotPlan       string                   `json:"copilot_plan"`
	AccessTypeSKU     string                   `json:"access_type_sku"`
	TokenBasedBilling bool                     `json:"token_based_billing"`
	QuotaResetDate    string                   `json:"quota_reset_date"`
	QuotaSnapshots    map[string]QuotaSnapshot `json:"quota_snapshots"`
	TimestampUTC      string                   `json:"timestamp_utc"`
}

// QuotaSnapshot is one row of the quota_snapshots map. Keys observed
// in the wild: `chat`, `premium_interactions`, `completions`.
//
// `Unlimited=true` with `entitlement: 0` is what an organisation seat
// reports: GitHub gives the member no personal allowance to measure
// against (the organisation's budget is the limit), which is not the same
// as having unlimited headroom.
type QuotaSnapshot struct {
	Entitlement int     `json:"entitlement"`
	Remaining   float64 `json:"remaining"`
	// PercentRemaining is nil when the bucket does not report it, so a
	// missing share is never read as 0% left.
	PercentRemaining  *float64 `json:"percent_remaining"`
	OverageCount      int      `json:"overage_count"`
	OveragePermitted  bool     `json:"overage_permitted"`
	Unlimited         bool     `json:"unlimited"`
	TokenBasedBilling bool     `json:"token_based_billing"`
	CreditsUsed       *float64 `json:"credits_used"`
}

// Metered reports whether the bucket has an allowance of its own to be
// measured against.
func (s QuotaSnapshot) Metered() bool { return !s.Unlimited && s.Entitlement > 0 }
