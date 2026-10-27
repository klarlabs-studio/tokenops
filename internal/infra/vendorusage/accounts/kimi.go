package accounts

import (
	"context"
	"fmt"
	"net/http"
	"time"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// readerKimi registers the reader (readers_gen.go).
func readerKimi() usage.Reader { return Kimi{} }

// Kimi reads Kimi Code's windows from GET /coding/v1/usages, the endpoint
// Moonshot's own client (MoonshotAI/kimi-code) calls; it is not in the
// published docs. The answer comes in two shapes, both read (CodexBar's
// docs/kimi.md):
//
//   - ratio pools under usages: used_ratio is a fraction, keyed limit_5h,
//     limit_7d, limit_month_total and limit_month_code (Kimi Code's share
//     of the monthly pool, a sub-window of month);
//   - legacy counts: usage (the weekly quota) and limits[] with a
//     window.duration, each with its detail limit and used counts.
//
// A ratio pool wins over a legacy window of the same name. A missing
// window is absent, not an error (new plans have no weekly window), but
// an answer with no window at all, or a figure that cannot be read, is.
type Kimi struct {
	BaseURL string
	HTTP    *http.Client
}

func (Kimi) Endpoint() string               { return "kimi" }
func (Kimi) Provider() eventschema.Provider { return "kimi" }
func (Kimi) Source() string                 { return "kimi-account" }

// kimiRatio is a ratio pool's figure.
type kimiRatio struct {
	UsedRatio number `json:"used_ratio"`
	ResetTime string `json:"reset_time"`
}

// kimiCount is a legacy count against its limit.
type kimiCount struct {
	Limit     number `json:"limit"`
	Used      number `json:"used"`
	ResetTime string `json:"resetTime"`
}

// kimiLegacyLimit is one entry of the legacy limits array.
type kimiLegacyLimit struct {
	Window struct {
		Duration number `json:"duration"`
		TimeUnit string `json:"timeUnit"`
	} `json:"window"`
	Detail *kimiCount `json:"detail"`
}

// kimiAnswer is the usages answer, in either shape.
type kimiAnswer struct {
	Usages map[string]*kimiRatio `json:"usages"`
	Usage  *kimiCount            `json:"usage"`
	Limits []kimiLegacyLimit     `json:"limits"`
}

// kimiRatioKeys are the ratio pools, with their window names and lengths.
var kimiRatioKeys = []struct {
	key, name string
	d         time.Duration
}{
	{"limit_5h", "5h", 5 * time.Hour},
	{"limit_7d", "week", 7 * 24 * time.Hour},
	{"limit_month_total", "month", 30 * 24 * time.Hour},
	{"limit_month_code", "month_code", 30 * 24 * time.Hour},
}

func (k Kimi) Read(ctx context.Context, key string) (usage.Reading, error) {
	var resp kimiAnswer
	if err := getJSON(ctx, k.HTTP, base(k.BaseURL, "https://api.kimi.com")+"/coding/v1/usages", key, &resp); err != nil {
		return usage.Reading{}, err
	}
	windows, err := kimiRatioWindows(resp.Usages)
	if err != nil {
		return usage.Reading{}, err
	}
	legacy, err := kimiLegacyWindows(resp)
	if err != nil {
		return usage.Reading{}, err
	}
	for _, w := range legacy {
		if !kimiHasWindow(windows, w.Name) {
			windows = append(windows, w)
		}
	}
	if len(windows) == 0 {
		return usage.Reading{}, fmt.Errorf("accounts: Kimi usage in a shape this version cannot read (no usage window)")
	}
	return usage.Reading{Scope: "account", Subscription: true, Windows: windows}, nil
}

// kimiRatioWindows reads the ratio pools present. An absent pool is
// skipped; a pool present with an unreadable ratio is an error.
func kimiRatioWindows(pools map[string]*kimiRatio) ([]usage.Window, error) {
	var out []usage.Window
	for _, p := range kimiRatioKeys {
		l, ok := pools[p.key]
		if !ok {
			continue
		}
		if l == nil || !l.UsedRatio.ok {
			return nil, fmt.Errorf("accounts: Kimi %s in a shape this version cannot read", p.key)
		}
		out = append(out, usage.Window{Name: p.name, UsedPct: clampPct(l.UsedRatio.v * 100), Duration: p.d, ResetsAt: parseTime(l.ResetTime)})
	}
	return out, nil
}

// kimiLegacyWindows reads the legacy count windows: usage as the weekly
// quota, and each limits[] entry by its duration.
func kimiLegacyWindows(resp kimiAnswer) ([]usage.Window, error) {
	var out []usage.Window
	for _, l := range resp.Limits {
		d, err := kimiUnit(l.Window.TimeUnit, l.Window.Duration)
		if err != nil || l.Detail == nil {
			return nil, fmt.Errorf("accounts: Kimi limits entry in a shape this version cannot read")
		}
		if w, ok := kimiCountWindow(kimiWindowName(d), d, *l.Detail); ok {
			out = append(out, w)
		}
	}
	if resp.Usage != nil {
		if w, ok := kimiCountWindow("week", 7*24*time.Hour, *resp.Usage); ok {
			out = append(out, w)
		}
	}
	return out, nil
}

// kimiCountWindow is a window from a count; a zero or absent limit has
// no window.
func kimiCountWindow(name string, d time.Duration, c kimiCount) (usage.Window, bool) {
	if !c.Limit.ok || c.Limit.v <= 0 {
		return usage.Window{}, false
	}
	return usage.Window{Name: name, UsedPct: clampPct(pct(c.Used.v, c.Limit.v)), Duration: d, ResetsAt: parseTime(c.ResetTime)}, true
}

// kimiUnit is a legacy duration in its time unit.
func kimiUnit(unit string, n number) (time.Duration, error) {
	var per time.Duration
	switch unit {
	case "TIME_UNIT_SECOND":
		per = time.Second
	case "TIME_UNIT_MINUTE":
		per = time.Minute
	case "TIME_UNIT_HOUR":
		per = time.Hour
	case "TIME_UNIT_DAY":
		per = 24 * time.Hour
	default:
		return 0, fmt.Errorf("accounts: Kimi time unit %q", unit)
	}
	if !n.ok || n.v <= 0 {
		return 0, fmt.Errorf("accounts: Kimi window duration")
	}
	return time.Duration(n.v * float64(per)), nil
}

// kimiWindowName names a window in words: "5h", "week", "month", or hours.
func kimiWindowName(d time.Duration) string {
	switch d {
	case 5 * time.Hour:
		return "5h"
	case 7 * 24 * time.Hour:
		return "week"
	case 30 * 24 * time.Hour:
		return "month"
	}
	return fmt.Sprintf("%.0fh", d.Hours())
}

// kimiHasWindow reports whether a window of that name was read.
func kimiHasWindow(ws []usage.Window, name string) bool {
	for _, w := range ws {
		if w.Name == name {
			return true
		}
	}
	return false
}
