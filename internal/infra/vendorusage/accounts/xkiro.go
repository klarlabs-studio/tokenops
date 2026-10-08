package accounts

import (
	"context"
	"errors"
	"net/http"
	"time"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// readerXKiro registers the reader (readers_gen.go).
func readerXKiro() usage.Reader { return XKiro{} }

// XKiro reads GET /v1/usage (https://docs.xkiro.com/api/usage/): the
// plan's spend windows, today's free-token allowance, which CodexBar's
// xKiro provider reads, and the wallet balance. xKiro documents the call as
// free (no tokens, rate limit or spend) and the free-token counter as
// resetting at 00:00 UTC. Figures are the account's, whichever of its keys
// asks.
type XKiro struct {
	BaseURL string
	HTTP    *http.Client
	// Now is the clock resets are computed from; nil is time.Now.
	Now func() time.Time
}

func (XKiro) Endpoint() string               { return "xkiro" }
func (XKiro) Provider() eventschema.Provider { return "xkiro" }
func (XKiro) Source() string                 { return "xkiro-account" }

func (x XKiro) Read(ctx context.Context, key string) (usage.Reading, error) {
	var resp struct {
		Object  string  `json:"object"`
		Plan    *string `json:"plan"`
		Windows []struct {
			WindowSec   number `json:"window_sec"`
			SpentUSD    number `json:"spent_usd"`
			CapUSD      number `json:"cap_usd"`
			ResetsInSec number `json:"resets_in_sec"`
		} `json:"windows"`
		FreeTokens *struct {
			UsedToday   number `json:"used_today"`
			LimitPerDay number `json:"limit_per_day"`
		} `json:"free_tokens"`
		Wallet *struct {
			BalanceUSD number `json:"balance_usd"`
		} `json:"wallet"`
	}
	if err := getJSON(ctx, x.HTTP, base(x.BaseURL, "https://api.xkiro.com")+"/v1/usage", key, &resp); err != nil {
		return usage.Reading{}, err
	}
	if resp.Object != "usage" || resp.FreeTokens == nil {
		return usage.Reading{}, errors.New("accounts: GET api.xkiro.com/v1/usage: unrecognised answer")
	}
	now := time.Now
	if x.Now != nil {
		now = x.Now
	}
	at := now().UTC()
	// plan null is pay-as-you-go, where the wallet is the only limit.
	r := usage.Reading{Scope: "account", Subscription: resp.Plan != nil}
	for _, w := range resp.Windows {
		if !w.WindowSec.ok || !w.CapUSD.ok || w.CapUSD.v <= 0 {
			continue
		}
		d := time.Duration(w.WindowSec.v) * time.Second
		win := usage.Window{Name: windowName(d), UsedPct: clampPct(pct(w.SpentUSD.v, w.CapUSD.v)), Duration: d}
		if w.ResetsInSec.ok {
			// To the minute, so an unchanged window reads the same each poll.
			win.ResetsAt = at.Add(time.Duration(w.ResetsInSec.v) * time.Second).Round(time.Minute)
		}
		r.Windows = append(r.Windows, win)
	}
	// A null daily limit is "no cap": no percentage is invented.
	if f := resp.FreeTokens; f.UsedToday.ok && f.LimitPerDay.ok {
		used := 100.0
		if f.LimitPerDay.v > 0 {
			used = clampPct(pct(f.UsedToday.v, f.LimitPerDay.v))
		}
		day := 24 * time.Hour
		r.Windows = append(r.Windows, usage.Window{Name: "free tokens", UsedPct: used, Duration: day, ResetsAt: at.Truncate(day).Add(day)})
	}
	if resp.Wallet != nil && resp.Wallet.BalanceUSD.ok {
		r.BalanceUSD, r.HasBalance = resp.Wallet.BalanceUSD.v, true
	}
	return r, nil
}
