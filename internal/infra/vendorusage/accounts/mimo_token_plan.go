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

// readerMiMoTokenPlan registers the reader (readers_gen.go).
func readerMiMoTokenPlan() usage.Reader { return MiMoTokenPlan{} }

// MiMoTokenPlan reads Xiaomi MiMo's Token Plan with its API key (tp-…),
// ported from CodexBar (Sources/CodexBarCore/Resources/Plugins/mimo.ts,
// 94cc400e1b): GET token-plan-{cn|sgp}.xiaomimimo.com/v1/platform/
// token_plan. The credits used of the plan's total are the window,
// resetting at the period's end; Xiaomi gives its times in UTC without a
// zone. The region is cn unless the credential starts "sgp:".
type MiMoTokenPlan struct {
	// BaseURL overrides the regional host; for tests.
	BaseURL string
	HTTP    *http.Client
}

func (MiMoTokenPlan) Endpoint() string               { return "mimo" }
func (MiMoTokenPlan) Provider() eventschema.Provider { return "mimo" }
func (MiMoTokenPlan) Source() string                 { return "mimo-token-plan" }
func (MiMoTokenPlan) KeyOnly() bool                  { return true }

var errMiMoPlanShape = errors.New("accounts: mimo token plan: unrecognised answer")

func (m MiMoTokenPlan) Read(ctx context.Context, credential string) (usage.Reading, error) {
	if isSession(credential) {
		return usage.Reading{}, usage.ErrSkip // the platform session is MiMo's
	}
	region, key := "cn", strings.TrimSpace(credential)
	if r, k, ok := strings.Cut(key, ":"); ok && (r == "cn" || r == "sgp") {
		region, key = r, strings.TrimSpace(k)
	}
	if key == "" {
		return usage.Reading{}, fmt.Errorf("%w (no MiMo Token Plan key)", usage.ErrAuth)
	}
	var resp struct {
		Object string `json:"object"`
		Period *struct {
			Start string `json:"start_at"`
			End   string `json:"end_at"`
		} `json:"period"`
		Quota *struct {
			Total     *int64 `json:"total_credits"`
			Used      *int64 `json:"used_credits"`
			Remaining *int64 `json:"remaining_credits"`
		} `json:"quota"`
	}
	u := base(m.BaseURL, "https://token-plan-"+region+".xiaomimimo.com") + "/v1/platform/token_plan"
	if err := getJSON(ctx, m.HTTP, u, key, &resp); err != nil {
		return usage.Reading{}, err
	}
	q := resp.Quota
	if resp.Object != "token_plan" || q == nil || q.Total == nil || q.Used == nil || q.Remaining == nil ||
		*q.Total < 0 || *q.Used < 0 || *q.Remaining != max(0, *q.Total-*q.Used) {
		return usage.Reading{}, errMiMoPlanShape
	}
	r := usage.Reading{Scope: "account", Subscription: true}
	if *q.Total == 0 {
		return r, nil
	}
	w := usage.Window{Name: "credits", UsedPct: pct(float64(*q.Used), float64(*q.Total))}
	if resp.Period != nil {
		start, err1 := mimoUTC(resp.Period.Start)
		end, err2 := mimoUTC(resp.Period.End)
		if err1 != nil || err2 != nil || (!start.IsZero() && !end.IsZero() && !end.After(start)) {
			return usage.Reading{}, errMiMoPlanShape
		}
		w.ResetsAt = end
		if !start.IsZero() && !end.IsZero() {
			w.Duration = end.Sub(start)
			w.Name = windowName(w.Duration)
		}
	}
	r.Windows = []usage.Window{w}
	return r, nil
}

// mimoUTC reads "2006-01-02 15:04:05" as UTC; "" is no time.
func mimoUTC(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	return time.Parse("2006-01-02 15:04:05", s)
}
