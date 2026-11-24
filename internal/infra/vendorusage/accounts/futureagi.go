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

// readerFutureAGI registers the reader (readers_gen.go).
func readerFutureAGI() usage.Reader { return FutureAGI{} }

// FutureAGI reads GET api.futureagi.com/usage/v2/usage-overview/, ported
// from CodexBar (Sources/CodexBarCore/Resources/Plugins/futureagi.ts,
// Providers/FutureAGI, docs/futureagi.md), with the API key in X-Api-Key
// and the secret key in X-Secret-Key: the credential is API_KEY:SECRET_KEY.
// AI credits and gateway requests against the free allowances the answer
// gives are windows over the billing period it names, resetting at its
// end. The allowances are not hard limits: paid use goes on past them.
type FutureAGI struct {
	BaseURL string
	HTTP    *http.Client
}

func (FutureAGI) Endpoint() string               { return "futureagi" }
func (FutureAGI) Provider() eventschema.Provider { return "futureagi" }
func (FutureAGI) Source() string                 { return "futureagi-account" }

var errFutureAGIShape = errors.New("accounts: future agi usage overview: unrecognised answer")

// futureAGIWindows are the meters read, by key, and what they count.
var futureAGIWindows = []struct{ key, name string }{
	{"ai_credits", "AI credits"},
	{"gateway_requests", "gateway requests"},
}

func (f FutureAGI) Read(ctx context.Context, credential string) (usage.Reading, error) {
	key, secret, ok := strings.Cut(strings.TrimSpace(credential), ":")
	key, secret = strings.TrimSpace(key), strings.TrimSpace(secret)
	if !ok || key == "" || secret == "" {
		return usage.Reading{}, fmt.Errorf("%w (Future AGI needs API_KEY:SECRET_KEY)", usage.ErrAuth)
	}
	var resp struct {
		Status *bool `json:"status"`
		Result *struct {
			Start      string `json:"billing_period_start"`
			End        string `json:"billing_period_end"`
			Dimensions []struct {
				Key       string   `json:"key"`
				Usage     *float64 `json:"current_usage"`
				Allowance *float64 `json:"free_allowance"`
			} `json:"dimensions"`
		} `json:"result"`
	}
	if err := doJSON(ctx, f.HTTP, http.MethodGet, base(f.BaseURL, "https://api.futureagi.com")+"/usage/v2/usage-overview/",
		http.Header{"X-Api-Key": {key}, "X-Secret-Key": {secret}}, nil, &resp); err != nil {
		return usage.Reading{}, err
	}
	res := resp.Result
	if resp.Status == nil || !*resp.Status || res == nil || len(res.Dimensions) == 0 || len(res.Dimensions) > 16 {
		return usage.Reading{}, errFutureAGIShape
	}
	start, end := futureAGIDate(res.Start), futureAGIDate(res.End)
	if start.IsZero() || end.IsZero() || !end.After(start) {
		return usage.Reading{}, errFutureAGIShape
	}
	meters := map[string][2]float64{}
	for _, d := range res.Dimensions {
		if d.Key == "" || d.Usage == nil || d.Allowance == nil || *d.Usage < 0 || *d.Allowance < 0 {
			return usage.Reading{}, errFutureAGIShape
		}
		if _, dup := meters[d.Key]; dup {
			return usage.Reading{}, errFutureAGIShape
		}
		meters[d.Key] = [2]float64{*d.Usage, *d.Allowance}
	}
	r := usage.Reading{Scope: "account", Subscription: true}
	dur := end.Sub(start)
	for _, w := range futureAGIWindows {
		m, ok := meters[w.key]
		if !ok || m[1] <= 0 {
			continue
		}
		r.Windows = append(r.Windows, usage.Window{Name: windowName(dur) + " (" + w.name + ")",
			UsedPct: clampPct(pct(m[0], m[1])), Duration: dur, ResetsAt: end})
	}
	return r, nil
}

// futureAGIDate reads a date or an RFC 3339 time.
func futureAGIDate(s string) time.Time {
	if t, err := time.Parse("2006-01-02", s); err == nil {
		return t
	}
	t, _ := time.Parse(time.RFC3339Nano, s)
	return t
}
