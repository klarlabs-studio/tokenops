package accounts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// readerChatbox registers the reader (readers_gen.go).
func readerChatbox() usage.Reader { return Chatbox{} }

// Chatbox reads a Chatbox AI licence's quota, ported from CodexBar
// (Sources/CodexBarCore/Resources/Plugins/chatbox.ts, Providers/Chatbox,
// docs/chatbox.md), as Chatbox's public client reads it: GET
// api.chatboxai.app/api/license/detail/realtime with the licence key as
// the Authorization header, no scheme. The licence key can also authorise
// AI requests; only this read is made with it.
//
// The plan's pool is the "plan" window, resetting at the next refresh the
// licence names (only before the plan expires); top-up credits are the
// "top-up credits" window, with no reset. Points and image counts are not
// money.
type Chatbox struct {
	BaseURL string
	HTTP    *http.Client
	// Now is the clock; nil uses time.Now.
	Now func() time.Time
}

func (Chatbox) Endpoint() string               { return "chatbox" }
func (Chatbox) Provider() eventschema.Provider { return "chatbox" }
func (Chatbox) Source() string                 { return "chatbox-license" }

var errChatboxShape = errors.New("accounts: chatbox licence: unrecognised answer")

type chatboxDetail struct {
	Type  string          `json:"type"`
	Usage json.RawMessage `json:"token_usage"`
	Limit json.RawMessage `json:"token_limit"`
}

func (c Chatbox) Read(ctx context.Context, key string) (usage.Reading, error) {
	key = strings.TrimSpace(key)
	if key == "" || len(key) > 4096 || strings.IndexFunc(key, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0 {
		return usage.Reading{}, fmt.Errorf("%w (not a Chatbox AI licence key)", usage.ErrAuth)
	}
	var resp struct {
		Data *struct {
			Details         *[]chatboxDetail `json:"unified_token_usage_details"`
			UnifiedUsage    json.RawMessage  `json:"unified_token_usage"`
			UnifiedLimit    json.RawMessage  `json:"unified_token_limit"`
			ExpansionUsage  json.RawMessage  `json:"expansion_pack_usage"`
			ExpansionLimit  json.RawMessage  `json:"expansion_pack_limit"`
			Status          *string          `json:"status"`
			TokenExpireTime string           `json:"token_expire_time"`
			ExpiresAt       string           `json:"expires_at"`
			NextRefresh     string           `json:"token_next_refresh_time"`
		} `json:"data"`
		Error json.RawMessage `json:"error"`
	}
	if err := doJSON(ctx, c.HTTP, http.MethodGet, base(c.BaseURL, "https://api.chatboxai.app")+"/api/license/detail/realtime",
		http.Header{"Authorization": {key}, "Cache-Control": {"no-store"}}, nil, &resp); err != nil {
		return usage.Reading{}, err
	}
	d := resp.Data
	if d == nil || (len(resp.Error) > 0 && string(resp.Error) != "null") {
		return usage.Reading{}, errChatboxShape
	}
	var plan *[2]float64
	if d.Details != nil {
		if len(*d.Details) > 100 {
			return usage.Reading{}, errChatboxShape
		}
		for _, e := range *d.Details {
			if e.Type == "" {
				return usage.Reading{}, errChatboxShape
			}
			if e.Type != "plan" {
				continue
			}
			p, ok := chatboxPool(e.Usage, e.Limit)
			if !ok || plan != nil {
				return usage.Reading{}, errChatboxShape
			}
			plan = p
		}
	}
	unified, ok1 := chatboxPool(d.UnifiedUsage, d.UnifiedLimit)
	expansion, ok2 := chatboxPool(d.ExpansionUsage, d.ExpansionLimit)
	if !ok1 || !ok2 || (plan == nil && unified == nil && expansion == nil) {
		return usage.Reading{}, errChatboxShape
	}
	now := time.Now
	if c.Now != nil {
		now = c.Now
	}
	t := now()
	expires := parseTime(d.TokenExpireTime)
	if expires.IsZero() {
		expires = parseTime(d.ExpiresAt)
	}
	usable := (d.Status == nil || *d.Status == "active") && (expires.IsZero() || expires.After(t))
	r := usage.Reading{Scope: "license", Subscription: true}
	if usable && plan != nil && plan[1] > 0 {
		w := usage.Window{Name: "plan", UsedPct: clampPct(pct(plan[0], plan[1]))}
		if next := parseTime(d.NextRefresh); next.After(t) && (expires.IsZero() || next.Before(expires)) {
			w.ResetsAt = next
		}
		r.Windows = append(r.Windows, w)
	}
	if expansion != nil && expansion[1] > 0 {
		r.Windows = append(r.Windows, usage.Window{Name: "top-up credits", UsedPct: clampPct(pct(expansion[0], expansion[1]))})
	}
	return r, nil
}

// chatboxPool reads a used and a limit counter: nil when either is absent,
// ok false when one is not a non-negative whole number.
func chatboxPool(usedRaw, limitRaw json.RawMessage) (*[2]float64, bool) {
	used, hasUsed, ok1 := optionalCount(usedRaw)
	limit, hasLimit, ok2 := optionalCount(limitRaw)
	if !ok1 || !ok2 {
		return nil, false
	}
	if !hasUsed || !hasLimit {
		return nil, true
	}
	return &[2]float64{used, limit}, true
}
