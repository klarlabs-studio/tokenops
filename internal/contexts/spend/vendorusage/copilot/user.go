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
type UserResponse struct {
	Login          string                   `json:"login"`
	ChatEnabled    bool                     `json:"chat_enabled"`
	QuotaResetDate string                   `json:"quota_reset_date"`
	QuotaSnapshots map[string]QuotaSnapshot `json:"quota_snapshots"`
	TimestampUTC   string                   `json:"timestamp_utc"`
}

// QuotaSnapshot is one row of the quota_snapshots map. Keys observed
// in the wild: `chat`, `premium_interactions`, `completions`.
// `Unlimited=true` means the operator is on a plan with no cap
// (typically Copilot Business / Enterprise); UI shows ∞.
type QuotaSnapshot struct {
	Entitlement      int     `json:"entitlement"`
	Remaining        float64 `json:"remaining"`
	PercentRemaining float64 `json:"percent_remaining"`
	OverageCount     int     `json:"overage_count"`
	Unlimited        bool    `json:"unlimited"`
}
