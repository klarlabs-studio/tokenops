package accounts

import (
	"context"
	"errors"
	"net/http"
	"time"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// readerFactory registers the reader (readers_gen.go).
func readerFactory() usage.Reader { return Factory{} }

// Factory reads Factory's (the Droid CLI's) token rate limits from GET
// /api/billing/limits on api.factory.ai, as CodexBar's Factory provider
// does with an API key: the standard pool's 5-hour, weekly and monthly
// windows, and the extra-usage balance.
type Factory struct {
	BaseURL string
	HTTP    *http.Client
	// Now is the clock a window's end is compared with; nil is time.Now.
	Now func() time.Time
}

func (Factory) Endpoint() string               { return "factory" }
func (Factory) Provider() eventschema.Provider { return "factory" }
func (Factory) Source() string                 { return "factory-account" }

// factoryWindow is one window as Factory reports it.
type factoryWindow struct {
	UsedPercent      number `json:"usedPercent"`
	WindowEnd        stamp  `json:"windowEnd"`
	SecondsRemaining number `json:"secondsRemaining"`
}

func (f Factory) Read(ctx context.Context, key string) (usage.Reading, error) {
	var resp struct {
		Limits *struct {
			Standard *struct {
				FiveHour *factoryWindow `json:"fiveHour"`
				Weekly   *factoryWindow `json:"weekly"`
				Monthly  *factoryWindow `json:"monthly"`
			} `json:"standard"`
		} `json:"limits"`
		ExtraUsageBalanceCents number `json:"extraUsageBalanceCents"`
	}
	header := http.Header{
		"Authorization":    {"Bearer " + key},
		"Origin":           {"https://app.factory.ai"},
		"Referer":          {"https://app.factory.ai/"},
		"x-factory-client": {"web-app"},
	}
	if err := doJSON(ctx, f.HTTP, http.MethodGet, base(f.BaseURL, "https://api.factory.ai")+"/api/billing/limits", header, nil, &resp); err != nil {
		return usage.Reading{}, err
	}
	if resp.Limits == nil && !resp.ExtraUsageBalanceCents.ok {
		return usage.Reading{}, errors.New("accounts: GET api.factory.ai/api/billing/limits: unexpected answer")
	}
	now := time.Now
	if f.Now != nil {
		now = f.Now
	}
	r := usage.Reading{Scope: "account"}
	if resp.Limits != nil && resp.Limits.Standard != nil {
		s := resp.Limits.Standard
		for _, w := range []struct {
			win *factoryWindow
			d   time.Duration
		}{{s.FiveHour, 5 * time.Hour}, {s.Weekly, 7 * 24 * time.Hour}, {s.Monthly, 30 * 24 * time.Hour}} {
			if w.win == nil || !w.win.UsedPercent.ok {
				continue
			}
			r.Windows = append(r.Windows, factoryReading(*w.win, w.d, now()))
		}
	}
	if resp.ExtraUsageBalanceCents.ok {
		r.BalanceUSD, r.HasBalance = resp.ExtraUsageBalanceCents.v/100, true
	}
	r.Subscription = len(r.Windows) > 0
	return r, nil
}

// factoryReading is one window: it resets after secondsRemaining, else at
// windowEnd; a window whose end has passed with no time remaining has
// rolled over and is unused, as CodexBar reads it.
func factoryReading(w factoryWindow, d time.Duration, now time.Time) usage.Window {
	out := usage.Window{Name: windowName(d), UsedPct: clampPct(w.UsedPercent.v), Duration: d}
	switch {
	case w.SecondsRemaining.ok && w.SecondsRemaining.v > 0:
		out.ResetsAt = now.Add(time.Duration(w.SecondsRemaining.v * float64(time.Second))).UTC()
	case !w.WindowEnd.t.IsZero() && w.WindowEnd.t.After(now):
		out.ResetsAt = w.WindowEnd.t
	case !w.WindowEnd.t.IsZero() && !w.SecondsRemaining.ok:
		out.UsedPct = 0
	}
	return out
}
