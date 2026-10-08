package accounts

import (
	"context"
	"net/http"
	"time"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// readerChutes registers the reader (readers_gen.go).
func readerChutes() usage.Reader { return Chutes{} }

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

func (c Chutes) Read(ctx context.Context, key string) (usage.Reading, error) {
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
		return usage.Reading{}, err
	}
	if !resp.Subscription {
		return usage.Reading{Scope: "account"}, nil
	}
	r := usage.Reading{Scope: "account", Subscription: true}
	for _, w := range []struct {
		name string
		d    time.Duration
		c    *capped
	}{{"4h", 4 * time.Hour, resp.FourHour}, {"month", 30 * 24 * time.Hour, resp.Monthly}} {
		if w.c == nil || w.c.Uncapped || !w.c.Cap.ok || w.c.Cap.v <= 0 {
			continue
		}
		r.Windows = append(r.Windows, usage.Window{Name: w.name, UsedPct: pct(w.c.Usage.v, w.c.Cap.v), Duration: w.d, ResetsAt: parseTime(w.c.ResetAt)})
	}
	return r, nil
}
