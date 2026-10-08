package accounts

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// readerZoomMate registers the reader (readers_gen.go).
func readerZoomMate() usage.Reader { return ZoomMate{} }

// ZoomMate reads the AI credits ZoomMate shows its signed-in users, ported
// from CodexBar (Sources/CodexBarCore/Resources/Plugins/zoommate.ts,
// Providers/ZoomMate, docs/zoommate.md). ZoomMate's web client exchanges
// the Zoom session cookies for a short-lived bearer token at
// /ai-computer/api/v1/login/ (the "nak"), then reads
// /ai-computer/api/v1/credits/status with it; this does the same on every
// read and keeps no token. The credential is the Cookie header of a
// request to ai.zoom.us, pasted at setup (Zoom's SSO cookies are not known
// by name), or a captured "Bearer ..." token, which expires within the
// hour.
//
// The credits used against the budget cap are a window over the billing
// cycle; the credits left are the balance. An unlimited budget has
// neither. The credit history CodexBar charts is not read.
type ZoomMate struct {
	BaseURL string
	HTTP    *http.Client
}

func (ZoomMate) Endpoint() string               { return "zoommate" }
func (ZoomMate) Provider() eventschema.Provider { return "zoommate" }
func (ZoomMate) Source() string                 { return "zoommate-account" }

func (z ZoomMate) Read(ctx context.Context, key string) (usage.Reading, error) {
	host := base(z.BaseURL, "https://ai.zoom.us") + "/ai-computer/api/v1/"
	header := http.Header{
		"Accept":          {"application/json, text/plain, */*"},
		"Accept-Language": {"en-US,en;q=0.9"},
		"User-Agent":      {browserUA},
		"Origin":          {"https://zoommate.zoom.us"},
		"Referer":         {"https://zoommate.zoom.us"},
	}
	key = strings.TrimSpace(key)
	switch {
	case len(key) > 7 && strings.EqualFold(key[:7], "bearer "):
		header.Set("Authorization", "Bearer "+strings.TrimSpace(key[7:]))
	case strings.Contains(key, "="):
		header.Set("Cookie", cookieHeader(key))
		var login struct {
			Data *struct {
				Nak string `json:"nak"`
			} `json:"data"`
		}
		if err := doJSON(ctx, z.HTTP, http.MethodGet, host+"login/?continue=https%3A%2F%2Fzoommate.zoom.us%2F", header, nil, &login); err != nil {
			return usage.Reading{}, err
		}
		if login.Data == nil || strings.TrimSpace(login.Data.Nak) == "" {
			return usage.Reading{}, fmt.Errorf("%w (ZoomMate's sign-in gave no token for this session)", usage.ErrAuth)
		}
		header.Set("Authorization", "Bearer "+strings.TrimPrefix(strings.TrimSpace(login.Data.Nak), "Bearer "))
	default:
		return usage.Reading{}, fmt.Errorf("%w (the ZoomMate credential is neither a Cookie header nor a bearer token)", usage.ErrAuth)
	}
	var resp struct {
		Data *struct {
			Status *struct {
				Cap        *number `json:"budget_cap"`
				Used       *number `json:"used_credit"`
				Remaining  *number `json:"remaining_credit"`
				Unlimited  bool    `json:"is_unlimited"`
				CycleStart stamp   `json:"cycle_start_date"`
				CycleEnd   stamp   `json:"cycle_end_date"`
			} `json:"credit_status"`
		} `json:"data"`
	}
	if err := doJSON(ctx, z.HTTP, http.MethodGet, host+"credits/status", header, nil, &resp); err != nil {
		return usage.Reading{}, err
	}
	if resp.Data == nil || resp.Data.Status == nil || resp.Data.Status.Cap == nil || resp.Data.Status.Used == nil {
		return usage.Reading{}, errors.New("accounts: zoommate credits/status: unrecognised answer")
	}
	s := resp.Data.Status
	r := usage.Reading{Scope: "account"}
	if s.Unlimited || s.Cap.v <= 0 {
		return r, nil
	}
	var d time.Duration
	if !s.CycleStart.t.IsZero() && s.CycleEnd.t.After(s.CycleStart.t) {
		d = s.CycleEnd.t.Sub(s.CycleStart.t).Round(time.Hour)
	}
	name := "cycle"
	if d > 0 {
		name = windowName(d)
	}
	r.Subscription = true
	r.Windows = []usage.Window{{Name: name, Duration: d, UsedPct: clampPct(pct(s.Used.v, s.Cap.v)), ResetsAt: s.CycleEnd.t}}
	if s.Remaining != nil && s.Remaining.ok {
		r.Credits, r.CreditsUnit, r.HasCredits = s.Remaining.v, "credits", true
	}
	return r, nil
}
