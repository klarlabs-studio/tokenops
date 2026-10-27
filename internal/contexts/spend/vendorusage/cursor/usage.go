// Package cursor polls Cursor's usage from cursor.com with the
// WorkosCursorSessionToken cookie the Cursor dashboard uses. Cursor
// publishes no contract for these endpoints; the shapes follow what the
// dashboard calls, as documented by CodexBar (docs/cursor.md) and
// community write-ups:
//
//   - GET /api/usage-summary: the billing cycle and the plan's included
//     usage in cents (individualUsage.plan), a team member's own cap
//     (individualUsage.overall) or a team's shared pool
//     (teamUsage.pooled). Read first.
//   - POST /api/dashboard/get-sand-usage-status: the weekly pool the
//     plans Cursor sells now share across Cursor and Grok (isUnified).
//     For those accounts usage-summary keeps reporting a stale monthly
//     cycle, so the pool is the reading.
//   - GET /api/usage?user=<id>: the legacy request counts
//     (numRequests/maxRequestUsage). Read only when usage-summary is
//     unavailable, and only an answer with a request allowance counts.
//
// Auth: cookie. Operators paste WorkosCursorSessionToken + user_id
// into config (or env); that is the only path wired. A future read of
// the Cursor app's own sign-in from state.vscdb must handle Cursor
// 3.27's encrypted cursorAuth/accessToken ("cursor-safe-storage:v1:"
// and Chromium v10 ciphertext, keyed by the "Cursor Safe Storage"
// Keychain item, as CodexBar #4790 found).
//
// Honesty caveat: this is ToS-grey territory. Cursor's own status-bar
// extension uses the endpoint so it's tolerated, but the contract
// can shift without notice. signal_quality marks any observation
// HIGH while telling consumers the source is undocumented.
//
// The package holds the response's value types, the poller and the
// mapping to envelopes; the HTTP client lives in
// internal/infra/vendorusage/cursor and reaches the poller through the
// UsageSource port.
package cursor

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"time"
)

// UsageSource fetches one usage snapshot. The HTTP client in
// internal/infra/vendorusage/cursor satisfies it.
type UsageSource interface {
	Usage(ctx context.Context) (*UsageResponse, error)
}

// ErrMissingCredential signals that either Cookie or UserID is
// unconfigured. Poller treats this as "stay idle" rather than fatal.
var ErrMissingCredential = errors.New("cursor: WorkosCursorSessionToken + user_id required")

// UsageResponse is one usage snapshot. The client sets Pool when the
// account's weekly pool is its plan, Summary when usage-summary answered,
// and Models (with StartOfMonth) only from the legacy /api/usage, whose
// payload it mirrors: the map keys are model identifiers (`gpt-4`,
// `premiumRequests`, …), each with its request count and cap.
type UsageResponse struct {
	Models       map[string]ModelUsage `json:"-"`
	StartOfMonth string                `json:"startOfMonth,omitempty"`
	Summary      *Summary              `json:"-"`
	Pool         *WeeklyPool           `json:"-"`
}

// Summary is GET /api/usage-summary. Amounts are in cents.
type Summary struct {
	BillingCycleStart string `json:"billingCycleStart"`
	BillingCycleEnd   string `json:"billingCycleEnd"`
	MembershipType    string `json:"membershipType"`
	LimitType         string `json:"limitType"`
	IsUnlimited       bool   `json:"isUnlimited"`
	IndividualUsage   struct {
		Plan    *Allowance `json:"plan"`
		Overall *Allowance `json:"overall"`
	} `json:"individualUsage"`
	TeamUsage struct {
		Pooled *Allowance `json:"pooled"`
	} `json:"teamUsage"`
}

// Allowance is one of usage-summary's blocks: used and limit in cents,
// and for the plan block the share Cursor itself computed.
type Allowance struct {
	Used             Number `json:"used"`
	Limit            Number `json:"limit"`
	TotalPercentUsed Number `json:"totalPercentUsed"`
}

// Scopes of a summary reading: whose allowance UsedPct measures.
const (
	ScopePlan = "plan" // the member's own plan
	ScopeUser = "user" // a team member's own cap
	ScopeTeam = "team" // the team's shared pool
)

// Reading is the share of the cycle's allowance used, taken from the
// first block that has one: the plan's own percentage, the plan's cents,
// a team member's cap, then the team's pool.
type Reading struct {
	UsedPct     float64
	Scope       string
	UsedCents   float64
	LimitCents  float64
	HasCents    bool
	HasUsedPct  bool
	IsUnlimited bool
}

// Read picks the summary's figure. ok is false when the answer holds
// none TokenOps can read and the plan is not unlimited: an unrecognised
// answer, not an empty one.
func (s *Summary) Read() (Reading, bool) {
	r := Reading{IsUnlimited: s.IsUnlimited}
	if p := s.IndividualUsage.Plan; p != nil {
		if p.Limit.OK && p.Used.OK {
			r.UsedCents, r.LimitCents, r.HasCents = p.Used.V, p.Limit.V, true
		}
		if p.TotalPercentUsed.OK {
			r.UsedPct, r.HasUsedPct, r.Scope = p.TotalPercentUsed.V, true, ScopePlan
			return r, true
		}
	}
	for _, c := range []struct {
		a     *Allowance
		scope string
	}{{s.IndividualUsage.Plan, ScopePlan}, {s.IndividualUsage.Overall, ScopeUser}, {s.TeamUsage.Pooled, ScopeTeam}} {
		if c.a == nil || !c.a.Used.OK || !c.a.Limit.OK || c.a.Limit.V <= 0 {
			continue
		}
		r.UsedCents, r.LimitCents, r.HasCents = c.a.Used.V, c.a.Limit.V, true
		r.UsedPct, r.HasUsedPct, r.Scope = c.a.Used.V/c.a.Limit.V*100, true, c.scope
		return r, true
	}
	return r, r.IsUnlimited
}

// WeeklyPool is the shared Cursor and Grok weekly pool from POST
// /api/dashboard/get-sand-usage-status, kept only when the account is on
// it (isUnified) and the answer has a share and a reset still ahead.
type WeeklyPool struct {
	UsedPct     float64
	PeriodStart time.Time
	ResetsAt    time.Time
	PlanName    string
}

// SandStatus is the get-sand-usage-status answer.
type SandStatus struct {
	CurrentPeriodStart    string `json:"currentPeriodStart"`
	NextResetTimestampUtc string `json:"nextResetTimestampUtc"`
	UsagePercent          Number `json:"usagePercent"`
	IsUnified             bool   `json:"isUnified"`
	CursorPlanName        string `json:"cursorPlanName"`
}

// Pool returns the weekly pool when the answer says the account is on
// one; nil otherwise, and for an answer without a share or a future
// reset, which leaves the account read from usage-summary.
func (s SandStatus) Pool(now time.Time) *WeeklyPool {
	if !s.IsUnified || !s.UsagePercent.OK {
		return nil
	}
	reset, err := time.Parse(time.RFC3339, s.NextResetTimestampUtc)
	if err != nil || !reset.After(now) {
		return nil
	}
	p := &WeeklyPool{UsedPct: s.UsagePercent.V, ResetsAt: reset.UTC(), PlanName: s.CursorPlanName}
	if start, err := time.Parse(time.RFC3339, s.CurrentPeriodStart); err == nil {
		p.PeriodStart = start.UTC()
	}
	return p
}

// HasAllowance reports whether a legacy answer carries a request
// allowance: an answer without one says nothing about the plan.
func (u *UsageResponse) HasAllowance() bool {
	for _, m := range u.Models {
		if m.MaxRequestUsage > 0 {
			return true
		}
	}
	return false
}

// Number decodes a JSON number or numeric string; OK is false when the
// field is absent, null or not a number.
type Number struct {
	V  float64
	OK bool
}

// UnmarshalJSON accepts 12, 12.5 and "12".
func (n *Number) UnmarshalJSON(b []byte) error {
	b = bytes.Trim(b, `"`)
	if len(b) == 0 || string(b) == "null" {
		return nil
	}
	if v, err := strconv.ParseFloat(string(b), 64); err == nil {
		n.V, n.OK = v, true
	}
	return nil
}

// ModelUsage is one row of the response. NumRequests is the in-window
// count; MaxRequestUsage is the plan's cap (0 or null = none).
type ModelUsage struct {
	NumRequests     int `json:"numRequests"`
	MaxRequestUsage int `json:"maxRequestUsage,omitempty"`
}

// UnmarshalJSON treats the response as a flat map keyed by model
// name. `startOfMonth` is hoisted into its own field; everything
// else lands in Models.
func (u *UsageResponse) UnmarshalJSON(data []byte) error {
	raw := map[string]json.RawMessage{}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	u.Models = make(map[string]ModelUsage, len(raw))
	for k, v := range raw {
		if k == "startOfMonth" {
			if err := json.Unmarshal(v, &u.StartOfMonth); err == nil {
				continue
			}
		}
		var m ModelUsage
		if err := json.Unmarshal(v, &m); err == nil {
			u.Models[k] = m
		}
	}
	return nil
}
