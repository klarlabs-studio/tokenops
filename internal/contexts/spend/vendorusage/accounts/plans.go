package accounts

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// The readers below were written against each vendor's published
// endpoint, or the vendor's own client source where the endpoint is not
// published, as of 2026-10-03; none had been run against a live key when
// they shipped. Each says which.

// number decodes a JSON number or a numeric string.
type number struct {
	v  float64
	ok bool
}

func (n *number) UnmarshalJSON(b []byte) error {
	b = bytes.Trim(b, `"`)
	if len(b) == 0 || string(b) == "null" {
		return nil
	}
	v, err := strconv.ParseFloat(string(b), 64)
	if err != nil {
		return nil // an unreadable figure is absent, not an error
	}
	n.v, n.ok = v, true
	return nil
}

// windowName names a window by its length: "5h", "week", "month".
func windowName(d time.Duration) string {
	switch {
	case d >= 28*24*time.Hour && d <= 31*24*time.Hour:
		return "month"
	case d == 7*24*time.Hour:
		return "week"
	case d == 24*time.Hour:
		return "day"
	case d > 0 && d%time.Hour == 0:
		return strconv.Itoa(int(d/time.Hour)) + "h"
	case d > 0:
		return strconv.Itoa(int(d/time.Minute)) + "m"
	}
	return "window"
}

// parseTime reads RFC 3339, or an ISO time without a zone as UTC.
func parseTime(s string) time.Time {
	s = strings.TrimSpace(s)
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05.999999999", "2006-01-02T15:04:05"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}

func pct(used, limit float64) float64 {
	if limit <= 0 {
		return 0
	}
	return used / limit * 100
}

// ZAI reads the GLM Coding Plan's windows from GET
// /api/monitor/usage/quota/limit. The endpoint is not in z.ai's API docs;
// it is what z.ai's own usage plugin (zai-org/zai-coding-plugins) calls,
// with the plan's key sent raw rather than as a bearer token.
type ZAI struct {
	BaseURL string
	HTTP    *http.Client
}

func (ZAI) Endpoint() string               { return "zai" }
func (ZAI) Provider() eventschema.Provider { return "zai" }
func (ZAI) Source() string                 { return "zai-account" }

// zaiUnits is the window length unit z.ai reports. Taken from community
// parsers; z.ai does not document it.
var zaiUnits = map[int]time.Duration{1: 24 * time.Hour, 3: time.Hour, 5: time.Minute, 6: 7 * 24 * time.Hour}

func (z ZAI) Read(ctx context.Context, key string) (Reading, error) {
	var resp struct {
		Success bool   `json:"success"`
		Code    int    `json:"code"`
		Msg     string `json:"msg"`
		Data    struct {
			Limits []struct {
				Type          string `json:"type"`
				Unit          int    `json:"unit"`
				Number        int    `json:"number"`
				Percentage    number `json:"percentage"`
				NextResetTime int64  `json:"nextResetTime"`
			} `json:"limits"`
		} `json:"data"`
	}
	if err := getJSONAuth(ctx, z.HTTP, base(z.BaseURL, "https://api.z.ai")+"/api/monitor/usage/quota/limit", key, &resp); err != nil {
		return Reading{}, err
	}
	if !resp.Success {
		// z.ai answers 200 with the refusal in the body: 1001 when no key
		// came, 1002 and up for a key it does not accept.
		if resp.Code >= 1000 && resp.Code < 1100 {
			return Reading{}, fmt.Errorf("%w (z.ai %d)", ErrAuth, resp.Code)
		}
		return Reading{}, fmt.Errorf("accounts: z.ai quota: %d %s", resp.Code, resp.Msg)
	}
	r := Reading{Scope: "key", Subscription: true}
	for _, l := range resp.Data.Limits {
		// TOKENS_LIMIT is the coding plan's 5-hour and weekly windows;
		// the MCP tool quota is not usage of the model.
		if l.Type != "TOKENS_LIMIT" || !l.Percentage.ok {
			continue
		}
		w := Window{UsedPct: l.Percentage.v}
		if unit, ok := zaiUnits[l.Unit]; ok && l.Number > 0 {
			w.Duration = unit * time.Duration(l.Number)
		}
		w.Name = windowName(w.Duration)
		if l.NextResetTime > 0 {
			w.ResetsAt = time.UnixMilli(l.NextResetTime).UTC()
		}
		r.Windows = append(r.Windows, w)
	}
	return r, nil
}

// Kimi reads Kimi Code's windows from GET /coding/v1/usages, the endpoint
// Moonshot's own client (MoonshotAI/kimi-code) calls; it is not in the
// published docs. used_ratio is a fraction.
type Kimi struct {
	BaseURL string
	HTTP    *http.Client
}

func (Kimi) Endpoint() string               { return "kimi" }
func (Kimi) Provider() eventschema.Provider { return "kimi" }
func (Kimi) Source() string                 { return "kimi-account" }

func (k Kimi) Read(ctx context.Context, key string) (Reading, error) {
	type limit struct {
		UsedRatio number `json:"used_ratio"`
		ResetTime string `json:"reset_time"`
	}
	var resp struct {
		Usages map[string]limit `json:"usages"`
	}
	if err := getJSON(ctx, k.HTTP, base(k.BaseURL, "https://api.kimi.com")+"/coding/v1/usages", key, &resp); err != nil {
		return Reading{}, err
	}
	r := Reading{Scope: "account", Subscription: true}
	for _, w := range []struct {
		key, name string
		d         time.Duration
	}{
		{"limit_5h", "5h", 5 * time.Hour},
		{"limit_7d", "week", 7 * 24 * time.Hour},
		{"limit_month_total", "month", 30 * 24 * time.Hour},
	} {
		l, ok := resp.Usages[w.key]
		if !ok || !l.UsedRatio.ok {
			continue
		}
		r.Windows = append(r.Windows, Window{Name: w.name, UsedPct: l.UsedRatio.v * 100, Duration: w.d, ResetsAt: parseTime(l.ResetTime)})
	}
	return r, nil
}

// MiniMax reads the Token Plan's windows from GET /v1/token_plan/remains,
// which MiniMax's Token Plan FAQ documents; the response fields are taken
// from MiniMax's own client, since the FAQ does not list them. MiniMax
// reports the share remaining.
type MiniMax struct {
	BaseURL string
	HTTP    *http.Client
}

func (MiniMax) Endpoint() string               { return "minimax" }
func (MiniMax) Provider() eventschema.Provider { return "minimax" }
func (MiniMax) Source() string                 { return "minimax-account" }

// minimaxUnlimited is the status of a window that does not apply.
const minimaxUnlimited = 3

// minimaxLoginFail is MiniMax's code for a missing or refused key, sent
// with HTTP 200 (seen 2026-10-03).
const minimaxLoginFail = 1004

func (m MiniMax) Read(ctx context.Context, key string) (Reading, error) {
	var resp struct {
		ModelRemains []struct {
			ModelName       string `json:"model_name"`
			StartTime       int64  `json:"start_time"`
			EndTime         int64  `json:"end_time"`
			IntervalLeftPct number `json:"current_interval_remaining_percent"`
			IntervalStatus  int    `json:"current_interval_status"`
			WeeklyEndTime   int64  `json:"weekly_end_time"`
			WeeklyLeftPct   number `json:"current_weekly_remaining_percent"`
			WeeklyStatus    int    `json:"current_weekly_status"`
		} `json:"model_remains"`
		BaseResp struct {
			StatusCode int    `json:"status_code"`
			StatusMsg  string `json:"status_msg"`
		} `json:"base_resp"`
	}
	if err := getJSON(ctx, m.HTTP, base(m.BaseURL, "https://api.minimax.io")+"/v1/token_plan/remains", key, &resp); err != nil {
		return Reading{}, err
	}
	switch resp.BaseResp.StatusCode {
	case 0:
	case minimaxLoginFail:
		// MiniMax answers 200 with the refusal in the body.
		return Reading{}, fmt.Errorf("%w (MiniMax %d)", ErrAuth, minimaxLoginFail)
	default:
		return Reading{}, fmt.Errorf("accounts: MiniMax remains: %d %s", resp.BaseResp.StatusCode, resp.BaseResp.StatusMsg)
	}
	r := Reading{Scope: "key", Subscription: true}
	if len(resp.ModelRemains) == 0 {
		return r, nil
	}
	// The text model's entry is the plan; the others are video and the like.
	e := resp.ModelRemains[0]
	for _, x := range resp.ModelRemains {
		if x.ModelName == "general" {
			e = x
			break
		}
	}
	if e.IntervalStatus != minimaxUnlimited && e.IntervalLeftPct.ok {
		w := Window{UsedPct: 100 - e.IntervalLeftPct.v}
		if e.EndTime > e.StartTime && e.StartTime > 0 {
			w.Duration = time.Duration(e.EndTime-e.StartTime) * time.Millisecond
		}
		w.Name = windowName(w.Duration)
		if e.EndTime > 0 {
			w.ResetsAt = time.UnixMilli(e.EndTime).UTC()
		}
		r.Windows = append(r.Windows, w)
	}
	if e.WeeklyStatus != minimaxUnlimited && e.WeeklyLeftPct.ok {
		w := Window{Name: "week", UsedPct: 100 - e.WeeklyLeftPct.v, Duration: 7 * 24 * time.Hour}
		if e.WeeklyEndTime > 0 {
			w.ResetsAt = time.UnixMilli(e.WeeklyEndTime).UTC()
		}
		r.Windows = append(r.Windows, w)
	}
	return r, nil
}

// Synthetic reads the subscription's request quota from GET /v2/quotas
// (https://dev.synthetic.new/docs/synthetic/quotas). Asking does not
// count against the quota.
type Synthetic struct {
	BaseURL string
	HTTP    *http.Client
}

func (Synthetic) Endpoint() string               { return "synthetic" }
func (Synthetic) Provider() eventschema.Provider { return "synthetic" }
func (Synthetic) Source() string                 { return "synthetic-account" }

func (s Synthetic) Read(ctx context.Context, key string) (Reading, error) {
	var resp struct {
		Subscription *struct {
			Limit    number `json:"limit"`
			Requests number `json:"requests"`
			RenewsAt string `json:"renewsAt"`
		} `json:"subscription"`
	}
	if err := getJSON(ctx, s.HTTP, base(s.BaseURL, "https://api.synthetic.new")+"/v2/quotas", key, &resp); err != nil {
		return Reading{}, err
	}
	sub := resp.Subscription
	if sub == nil || !sub.Limit.ok || sub.Limit.v <= 0 {
		return Reading{Scope: "account"}, nil
	}
	return Reading{Scope: "account", Subscription: true, Windows: []Window{{
		Name: "requests", UsedPct: pct(sub.Requests.v, sub.Limit.v), ResetsAt: parseTime(sub.RenewsAt),
	}}}, nil
}

// Chutes reads the subscription's 4-hour and monthly caps from GET
// /users/me/subscription_usage, an endpoint in Chutes' API spec; the
// fields come from Chutes' own source. Usage is in pay-as-you-go dollars
// against a cap in dollars.
type Chutes struct {
	BaseURL string
	HTTP    *http.Client
}

func (Chutes) Endpoint() string               { return "chutes" }
func (Chutes) Provider() eventschema.Provider { return "chutes" }
func (Chutes) Source() string                 { return "chutes-account" }

func (c Chutes) Read(ctx context.Context, key string) (Reading, error) {
	type capped struct {
		Usage    number `json:"usage"`
		Cap      number `json:"cap"`
		ResetAt  string `json:"reset_at"`
		Uncapped bool   `json:"uncapped"`
	}
	var resp struct {
		Subscription bool    `json:"subscription"`
		FourHour     *capped `json:"four_hour"`
		Monthly      *capped `json:"monthly"`
	}
	if err := getJSON(ctx, c.HTTP, base(c.BaseURL, "https://api.chutes.ai")+"/users/me/subscription_usage", key, &resp); err != nil {
		return Reading{}, err
	}
	if !resp.Subscription {
		return Reading{Scope: "account"}, nil
	}
	r := Reading{Scope: "account", Subscription: true}
	for _, w := range []struct {
		name string
		d    time.Duration
		c    *capped
	}{{"4h", 4 * time.Hour, resp.FourHour}, {"month", 30 * 24 * time.Hour, resp.Monthly}} {
		if w.c == nil || w.c.Uncapped || !w.c.Cap.ok || w.c.Cap.v <= 0 {
			continue
		}
		r.Windows = append(r.Windows, Window{Name: w.name, UsedPct: pct(w.c.Usage.v, w.c.Cap.v), Duration: w.d, ResetsAt: parseTime(w.c.ResetAt)})
	}
	return r, nil
}

// DeepInfra reads GET /payment/checklist, in DeepInfra's OpenAPI spec:
// spend since the last invoice (recent, USD), the account's limit, and
// whether it is suspended. A negative stripe_balance is prepaid credit.
type DeepInfra struct {
	BaseURL string
	HTTP    *http.Client
}

func (DeepInfra) Endpoint() string               { return "deepinfra" }
func (DeepInfra) Provider() eventschema.Provider { return "deepinfra" }
func (DeepInfra) Source() string                 { return "deepinfra-account" }

func (d DeepInfra) Read(ctx context.Context, key string) (Reading, error) {
	var resp struct {
		StripeBalance number `json:"stripe_balance"`
		Recent        number `json:"recent"`
		Limit         number `json:"limit"`
		Suspended     bool   `json:"suspended"`
	}
	if err := getJSON(ctx, d.HTTP, base(d.BaseURL, "https://api.deepinfra.com")+"/payment/checklist?compute_owed=true", key, &resp); err != nil {
		return Reading{}, err
	}
	r := Reading{Scope: "account", LimitReached: resp.Suspended}
	if resp.Recent.ok {
		r.UsedUSD, r.HasUsed = resp.Recent.v, true
	}
	if resp.Limit.ok && resp.Limit.v > 0 {
		r.LimitUSD = resp.Limit.v
	}
	if resp.StripeBalance.ok && resp.StripeBalance.v < 0 {
		r.BalanceUSD, r.HasBalance = -resp.StripeBalance.v, true
	}
	return r, nil
}

// Vercel reads the AI Gateway's credit balance from GET /v1/credits
// (https://vercel.com/docs/ai-gateway/sdks-and-apis/rest-api). It is the
// team's balance, not the key's; total_used is lifetime and not read.
type Vercel struct {
	BaseURL string
	HTTP    *http.Client
}

func (Vercel) Endpoint() string               { return "vercel" }
func (Vercel) Provider() eventschema.Provider { return "vercel" }
func (Vercel) Source() string                 { return "vercel-account" }

func (v Vercel) Read(ctx context.Context, key string) (Reading, error) {
	var resp struct {
		Balance number `json:"balance"`
	}
	if err := getJSON(ctx, v.HTTP, base(v.BaseURL, "https://ai-gateway.vercel.sh")+"/v1/credits", key, &resp); err != nil {
		return Reading{}, err
	}
	if !resp.Balance.ok {
		return Reading{}, errors.New("accounts: Vercel AI Gateway credits: no balance in the answer")
	}
	return Reading{Scope: "team", BalanceUSD: resp.Balance.v, HasBalance: true, LimitReached: resp.Balance.v <= 0}, nil
}
