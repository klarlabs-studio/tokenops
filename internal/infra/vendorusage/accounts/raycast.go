package accounts

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// readerRaycast registers the reader (readers_gen.go).
func readerRaycast() usage.Reader { return Raycast{} }

// Raycast reads the AI credits Raycast's account settings show, ported from
// CodexBar (Sources/CodexBarCore/Resources/Plugins/raycast.ts,
// Providers/Raycast, docs/raycast.md): GET
// www.raycast.com/frontend_api/current_user/ai_credits with the website
// session (__raycast_session, and csrf_token when present). Only those two
// cookies are sent. The desktop app's OAuth token is a different
// credential and is not used.
//
// The share of the month's allowance used is the "month" window,
// resetting when the next credits arrive; the credits left are the
// balance. Rollover can leave more than the grant: that is 0% used.
type Raycast struct {
	BaseURL string
	HTTP    *http.Client
}

func (Raycast) Endpoint() string               { return "raycast" }
func (Raycast) Provider() eventschema.Provider { return "raycast" }
func (Raycast) Source() string                 { return "raycast-account" }

func (r Raycast) Read(ctx context.Context, key string) (usage.Reading, error) {
	session := sessionToken(key, "__raycast_session")
	if session == "" {
		return usage.Reading{}, fmt.Errorf("%w (no __raycast_session in the Raycast session)", usage.ErrAuth)
	}
	cookie := "__raycast_session=" + session
	if csrf := cookieValue(key, "csrf_token"); csrf != "" {
		cookie += "; csrf_token=" + csrf
	}
	header := http.Header{
		"Cookie":     {cookie},
		"Origin":     {"https://www.raycast.com"},
		"Referer":    {"https://www.raycast.com/settings"},
		"User-Agent": {browserUA},
	}
	var resp struct {
		Remaining *number `json:"remaining_balance_credits"`
		Total     *number `json:"total_balance_credits"`
		Next      stamp   `json:"next_credits_at"`
	}
	url := base(r.BaseURL, "https://www.raycast.com") + "/frontend_api/current_user/ai_credits"
	if err := doJSON(ctx, r.HTTP, http.MethodGet, url, header, nil, &resp); err != nil {
		return usage.Reading{}, err
	}
	if (resp.Remaining == nil || !resp.Remaining.ok) && (resp.Total == nil || !resp.Total.ok) {
		return usage.Reading{}, errors.New("accounts: raycast ai_credits: unrecognised answer")
	}
	out := usage.Reading{Scope: "account"}
	if resp.Remaining != nil && resp.Remaining.ok {
		out.Credits, out.CreditsUnit, out.HasCredits = max(0, resp.Remaining.v), "credits", true
	}
	if resp.Remaining != nil && resp.Remaining.ok && resp.Total != nil && resp.Total.v > 0 {
		out.Subscription = true
		out.Windows = []usage.Window{{Name: "month", ResetsAt: resp.Next.t,
			UsedPct: clampPct(pct(max(0, resp.Total.v-resp.Remaining.v), resp.Total.v))}}
	}
	return out, nil
}
