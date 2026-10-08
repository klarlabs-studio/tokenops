package accounts

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strings"
	"time"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// readerZed registers the reader (readers_gen.go).
func readerZed() usage.Reader { return Zed{} }

// Zed reads the plan's edit-prediction allowance from GET
// /client/users/me on cloud.zed.dev, the call the Zed editor makes, with
// the editor's own sign-in: "Authorization: <user id> <access token>".
// `tokenops vendor-usage setup zed` reads that sign-in once from the
// Keychain (the internet password for https://zed.dev), with the
// operator's consent, and stores it; the daemon never reads the Keychain
// for it. The endpoint is not published; this follows CodexBar.
type Zed struct {
	BaseURL string
	HTTP    *http.Client
}

func (Zed) Endpoint() string               { return "zed" }
func (Zed) Provider() eventschema.Provider { return "zed" }
func (Zed) Source() string                 { return "zed-account" }

func (z Zed) Read(ctx context.Context, key string) (usage.Reading, error) {
	user, token, ok := strings.Cut(strings.TrimSpace(key), " ")
	if !ok || user == "" || strings.TrimSpace(token) == "" {
		return usage.Reading{}, fmt.Errorf("%w (Zed's sign-in is a user ID and an access token)", usage.ErrAuth)
	}
	var resp struct {
		Plan *struct {
			PlanV3             string `json:"plan_v3"`
			SubscriptionPeriod *struct {
				StartedAt string `json:"started_at"`
				EndedAt   string `json:"ended_at"`
			} `json:"subscription_period"`
			Usage struct {
				EditPredictions *zedAllowance `json:"edit_predictions"`
			} `json:"usage"`
		} `json:"plan"`
	}
	url := base(z.BaseURL, "https://cloud.zed.dev") + "/client/users/me"
	if err := getJSONAuth(ctx, z.HTTP, url, user+" "+strings.TrimSpace(token), &resp); err != nil {
		return usage.Reading{}, err
	}
	if resp.Plan == nil || resp.Plan.PlanV3 == "" {
		return usage.Reading{}, fmt.Errorf("accounts: Zed's account in a shape this version cannot read")
	}
	r := usage.Reading{Scope: "account", Subscription: true}
	ep := resp.Plan.Usage.EditPredictions
	if ep == nil || ep.Used == nil || *ep.Used < 0 {
		return r, nil
	}
	if limit, ok := ep.limit(); ok && limit > 0 {
		w := usage.Window{Name: "month (edit predictions)", UsedPct: clampPct(pct(*ep.Used, limit))}
		if p := resp.Plan.SubscriptionPeriod; p != nil {
			start, end := parseTime(p.StartedAt), parseTime(p.EndedAt)
			w.ResetsAt = end
			if !start.IsZero() && end.After(start) {
				w.Duration = end.Sub(start).Round(time.Hour)
			}
		}
		r.Windows = append(r.Windows, w)
	}
	return r, nil
}

// zedAllowance is edit_predictions: used, and a limit that is a number,
// {"limited": n}, or "unlimited".
type zedAllowance struct {
	Used  *float64        `json:"used"`
	Limit json.RawMessage `json:"limit"`
}

// limit is the allowance; ok is false for "unlimited" or an unknown shape.
func (a zedAllowance) limit() (float64, bool) {
	var n float64
	if json.Unmarshal(a.Limit, &n) == nil && !math.IsInf(n, 0) && n >= 0 {
		return n, true
	}
	var limited struct {
		Limited *float64 `json:"limited"`
	}
	if json.Unmarshal(a.Limit, &limited) == nil && limited.Limited != nil && *limited.Limited >= 0 {
		return *limited.Limited, true
	}
	return 0, false
}
