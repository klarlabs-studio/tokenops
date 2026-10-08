package accounts

import (
	"context"
	"fmt"
	"net/http"
	"time"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// readerMiniMax registers the reader (readers_gen.go).
func readerMiniMax() usage.Reader { return MiniMax{} }

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

func (m MiniMax) Read(ctx context.Context, key string) (usage.Reading, error) {
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
		return usage.Reading{}, err
	}
	switch resp.BaseResp.StatusCode {
	case 0:
	case minimaxLoginFail:
		// MiniMax answers 200 with the refusal in the body.
		return usage.Reading{}, fmt.Errorf("%w (MiniMax %d)", usage.ErrAuth, minimaxLoginFail)
	default:
		return usage.Reading{}, fmt.Errorf("accounts: MiniMax remains: %d %s", resp.BaseResp.StatusCode, resp.BaseResp.StatusMsg)
	}
	r := usage.Reading{Scope: "key", Subscription: true}
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
		w := usage.Window{UsedPct: 100 - e.IntervalLeftPct.v}
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
		w := usage.Window{Name: "week", UsedPct: 100 - e.WeeklyLeftPct.v, Duration: 7 * 24 * time.Hour}
		if e.WeeklyEndTime > 0 {
			w.ResetsAt = time.UnixMilli(e.WeeklyEndTime).UTC()
		}
		r.Windows = append(r.Windows, w)
	}
	return r, nil
}
