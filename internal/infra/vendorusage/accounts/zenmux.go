package accounts

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// readerZenMux registers the reader (readers_gen.go).
func readerZenMux() usage.Reader { return ZenMux{} }

// ZenMux reads the subscription's rolling 5-hour and 7-day quotas from GET
// /api/v1/management/subscription/detail and, best effort, the
// pay-as-you-go balance from GET /api/v1/management/payg/balance
// (https://docs.zenmux.ai/api/platform/subscription-detail), as CodexBar's
// ZenMux provider does. Both take a Management API key; the inference keys
// harnesses send ZenMux are refused there, so this reader takes keys for
// its own endpoint, "zenmux-management", and never the harnesses'.
type ZenMux struct {
	BaseURL string
	HTTP    *http.Client
}

func (ZenMux) Endpoint() string               { return "zenmux-management" }
func (ZenMux) Provider() eventschema.Provider { return "zenmux" }
func (ZenMux) Source() string                 { return "zenmux-account" }

type zenMuxQuota struct {
	UsagePercentage number `json:"usage_percentage"`
	ResetsAt        string `json:"resets_at"`
}

func (z ZenMux) Read(ctx context.Context, key string) (usage.Reading, error) {
	root := base(z.BaseURL, "https://zenmux.ai") + "/api/v1/management/"
	var detail struct {
		Success bool `json:"success"`
		Data    *struct {
			Quota5h *zenMuxQuota `json:"quota_5_hour"`
			Quota7d *zenMuxQuota `json:"quota_7_day"`
		} `json:"data"`
	}
	if err := getJSON(ctx, z.HTTP, root+"subscription/detail", key, &detail); err != nil {
		return usage.Reading{}, err
	}
	if !detail.Success || detail.Data == nil {
		return usage.Reading{}, errors.New("accounts: GET zenmux.ai/api/v1/management/subscription/detail: unsuccessful answer")
	}
	r := usage.Reading{Scope: "account"}
	for _, w := range []struct {
		q *zenMuxQuota
		d time.Duration
	}{{detail.Data.Quota5h, 5 * time.Hour}, {detail.Data.Quota7d, 7 * 24 * time.Hour}} {
		if w.q == nil || !w.q.UsagePercentage.ok {
			continue
		}
		// usage_percentage is a fraction.
		r.Windows = append(r.Windows, usage.Window{Name: windowName(w.d), UsedPct: clampPct(w.q.UsagePercentage.v * 100), Duration: w.d, ResetsAt: parseTime(w.q.ResetsAt)})
	}
	r.Subscription = len(r.Windows) > 0
	var balance struct {
		Success bool `json:"success"`
		Data    *struct {
			Currency     string `json:"currency"`
			TotalCredits number `json:"total_credits"`
		} `json:"data"`
	}
	// The balance is an extra: only a refused key fails the reading.
	switch err := getJSON(ctx, z.HTTP, root+"payg/balance", key, &balance); {
	case errors.Is(err, usage.ErrAuth):
		return usage.Reading{}, err
	case err == nil && balance.Success && balance.Data != nil && strings.EqualFold(strings.TrimSpace(balance.Data.Currency), "usd") && balance.Data.TotalCredits.ok:
		r.BalanceUSD, r.HasBalance = balance.Data.TotalCredits.v, true
	}
	return r, nil
}
