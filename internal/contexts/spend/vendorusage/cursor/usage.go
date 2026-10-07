// Package cursor polls Cursor's per-user usage endpoint
// (cursor.com/api/usage?user=<id>) using the WorkosCursorSessionToken
// cookie the Cursor IDE itself uses. Same data the IDE's status-bar
// usage indicator reads. The endpoint is internal — Cursor doesn't
// publish a contract — but it's stable enough that every third-party
// Cursor usage tracker (cursor-stats, cursor-usage-tracker,
// cursor_api_demo) relies on it.
//
// Auth: cookie. Operators paste WorkosCursorSessionToken + user_id
// into config (or env), or — future enhancement — TokenOps reads
// them from the local Cursor IDE state.vscdb SQLite store. For now
// the explicit-config path is the only one wired.
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
	"context"
	"encoding/json"
	"errors"
)

// UsageSource fetches one usage snapshot. The HTTP client in
// internal/infra/vendorusage/cursor satisfies it.
type UsageSource interface {
	Usage(ctx context.Context) (*UsageResponse, error)
}

// ErrMissingCredential signals that either Cookie or UserID is
// unconfigured. Poller treats this as "stay idle" rather than fatal.
var ErrMissingCredential = errors.New("cursor: WorkosCursorSessionToken + user_id required")

// UsageResponse mirrors the observed cursor.com /api/usage payload.
// The map keys are model identifiers (`gpt-4`, `gpt-4-32k`,
// `premiumRequests`, …); each value carries the per-window request
// count + the entitlement cap.
type UsageResponse struct {
	Models       map[string]ModelUsage `json:"-"`
	StartOfMonth string                `json:"startOfMonth,omitempty"`
}

// ModelUsage is one row of the response. NumRequests is the in-window
// count; MaxRequestUsage is the plan's cap (0 = unlimited).
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
