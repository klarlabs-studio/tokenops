package accounts

import (
	"context"
	"net/http"
	"time"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// readerKimi registers the reader (readers_gen.go).
func readerKimi() usage.Reader { return Kimi{} }

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

func (k Kimi) Read(ctx context.Context, key string) (usage.Reading, error) {
	type limit struct {
		UsedRatio number `json:"used_ratio"`
		ResetTime string `json:"reset_time"`
	}
	var resp struct {
		Usages map[string]limit `json:"usages"`
	}
	if err := getJSON(ctx, k.HTTP, base(k.BaseURL, "https://api.kimi.com")+"/coding/v1/usages", key, &resp); err != nil {
		return usage.Reading{}, err
	}
	r := usage.Reading{Scope: "account", Subscription: true}
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
		r.Windows = append(r.Windows, usage.Window{Name: w.name, UsedPct: l.UsedRatio.v * 100, Duration: w.d, ResetsAt: parseTime(l.ResetTime)})
	}
	return r, nil
}
