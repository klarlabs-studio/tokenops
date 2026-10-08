package accounts

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// readerXAI registers the reader (readers_gen.go).
func readerXAI() usage.Reader { return XAI{} }

// XAI reads a team's prepaid credit from xAI's Management API, GET
// /v1/billing/teams/{team_id}/prepaid/balance
// (https://docs.x.ai/developers/rest-api-reference/management/billing),
// and, best effort, its spend over the last 30 days from POST
// /v1/billing/teams/{team_id}/usage, as CodexBar's xAI provider does
// (Sources/CodexBarCore/Resources/Plugins/xai.js).
// The credential is "TEAM_ID:MANAGEMENT_KEY": the Management API needs
// both, and refuses inference keys. The ledger is inverted, in USD cents
// as a string (a $10 top-up is "-1000"), so the balance is the negated
// total. It is the posted ledger: spend is posted at the billing cycle's
// close, so mid-cycle it can be above the console's live figure.
//
// The spend is the sum of the daily USD figures of the 30 UTC days ending
// today, stored with its period. It is left out when the history cannot be
// read or xAI says it was cut short (limitReached): a partial sum would
// understate it. Only a refused key fails the reading.
type XAI struct {
	BaseURL string
	HTTP    *http.Client
	// Now is the clock the 30 days end at; nil is time.Now.
	Now func() time.Time
}

// xaiHistoryDays is how many UTC days of spend are read, today included.
const xaiHistoryDays = 30

func (XAI) Endpoint() string               { return "xai" }
func (XAI) Provider() eventschema.Provider { return eventschema.ProviderXAI }
func (XAI) Source() string                 { return "xai-account" }

func (x XAI) Read(ctx context.Context, credential string) (usage.Reading, error) {
	team, key, ok := strings.Cut(strings.TrimSpace(credential), ":")
	team, key = strings.TrimSpace(team), strings.TrimSpace(key)
	if !ok || team == "" || key == "" || strings.ContainsAny(team, "/?#") || team == "." || team == ".." {
		return usage.Reading{}, fmt.Errorf("%w (xAI needs TEAM_ID:MANAGEMENT_KEY)", usage.ErrAuth)
	}
	var resp struct {
		Total *struct {
			Val string `json:"val"`
		} `json:"total"`
	}
	root := base(x.BaseURL, "https://management-api.x.ai") + "/v1/billing/teams/" + url.PathEscape(team)
	if err := getJSON(ctx, x.HTTP, root+"/prepaid/balance", key, &resp); err != nil {
		var se *statusError
		if errors.As(err, &se) && se.status == http.StatusNotFound {
			return usage.Reading{}, fmt.Errorf("%w (xAI knows no team %q for this management key)", usage.ErrAuth, team)
		}
		return usage.Reading{}, err
	}
	if resp.Total == nil {
		return usage.Reading{}, errors.New("accounts: xai balance: no total")
	}
	cents, err := strconv.ParseFloat(strings.TrimSpace(resp.Total.Val), 64)
	if err != nil {
		return usage.Reading{}, errors.New("accounts: xai balance: unreadable total")
	}
	r := usage.Reading{Scope: "team", BalanceUSD: -cents / 100, HasBalance: true}
	switch used, err := x.spend(ctx, root, key); {
	case errors.Is(err, usage.ErrAuth):
		return usage.Reading{}, err
	case err == nil:
		r.UsedUSD, r.HasUsed, r.UsedPeriod = used, true, xaiHistoryDays*24*time.Hour
	}
	return r, nil
}

// xaiUsage is the answer of POST /v1/billing/teams/{team_id}/usage.
type xaiUsage struct {
	TimeSeries []struct {
		DataPoints []struct {
			Timestamp string     `json:"timestamp"`
			Values    []*float64 `json:"values"`
		} `json:"dataPoints"`
	} `json:"timeSeries"`
	LimitReached bool `json:"limitReached"`
}

// spend sums the team's daily USD spend over the last 30 UTC days.
func (x XAI) spend(ctx context.Context, root, key string) (float64, error) {
	now := time.Now
	if x.Now != nil {
		now = x.Now
	}
	end := now().UTC()
	start := time.Date(end.Year(), end.Month(), end.Day()-(xaiHistoryDays-1), 0, 0, 0, 0, time.UTC)
	const layout = "2006-01-02 15:04:05"
	payload := map[string]any{"analyticsRequest": map[string]any{
		"timeRange": map[string]any{"startTime": start.Format(layout), "endTime": end.Format(layout), "timezone": "Etc/GMT"},
		"timeUnit":  "TIME_UNIT_DAY",
		"values":    []any{map[string]any{"name": "usd", "aggregation": "AGGREGATION_SUM"}},
		"groupBy":   []any{},
		"filters":   []any{},
	}}
	var resp xaiUsage
	if err := postJSON(ctx, x.HTTP, root+"/usage", http.Header{"Authorization": {"Bearer " + key}}, payload, &resp); err != nil {
		return 0, err
	}
	return resp.total()
}

// total is the sum of every data point, or an error for an answer that is
// not a whole history.
func (u xaiUsage) total() (float64, error) {
	if u.TimeSeries == nil {
		return 0, errors.New("accounts: xai usage: no timeSeries")
	}
	if u.LimitReached {
		return 0, errors.New("accounts: xai usage: the history was cut short")
	}
	sum := 0.0
	for _, series := range u.TimeSeries {
		if series.DataPoints == nil {
			return 0, errors.New("accounts: xai usage: a series without dataPoints")
		}
		for _, p := range series.DataPoints {
			if parseTime(p.Timestamp).IsZero() || len(p.Values) == 0 || p.Values[0] == nil {
				return 0, errors.New("accounts: xai usage: unreadable data point")
			}
			if v := *p.Values[0]; v < 0 || math.IsInf(v, 0) || math.IsNaN(v) {
				return 0, errors.New("accounts: xai usage: unreadable data point")
			}
			sum += *p.Values[0]
		}
	}
	return sum, nil
}
