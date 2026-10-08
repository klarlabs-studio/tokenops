package accounts

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
)

// gatewayBifrost registers the gateway (gateways_gen.go).
func gatewayBifrost() usage.Gateway { return Bifrost{} }

// Bifrost reads GET /api/governance/virtual-keys/quota, the virtual key's
// own budgets (maximhq/bifrost docs/openapi): spend against each limit and
// how often it resets.
type Bifrost struct {
	// HTTP is the client; nil uses a default.
	HTTP *http.Client
}

func (Bifrost) Name() string   { return "bifrost" }
func (Bifrost) Source() string { return "bifrost-account" }

func (g Bifrost) Recognise(ctx context.Context, root string) bool {
	hc := g.HTTP
	body, ok := probe(ctx, hc, root+"/health")
	if !ok {
		return false
	}
	var h struct {
		Components map[string]any `json:"components"`
	}
	if json.Unmarshal(body, &h) != nil {
		return false
	}
	_, ok = h.Components["db_pings"]
	return ok
}

// bifrostDurations are Bifrost's reset durations.
var bifrostDurations = map[string]time.Duration{
	"1d": 24 * time.Hour, "1w": 7 * 24 * time.Hour, "1M": 30 * 24 * time.Hour, "1Y": 365 * 24 * time.Hour,
	"1h": time.Hour, "5m": 5 * time.Minute, "30s": 30 * time.Second,
}

func (g Bifrost) Read(ctx context.Context, root, key string) (usage.Reading, error) {
	hc := g.HTTP
	var resp struct {
		IsActive bool `json:"is_active"`
		Budgets  []struct {
			MaxLimit      number `json:"max_limit"`
			CurrentUsage  number `json:"current_usage"`
			ResetDuration string `json:"reset_duration"`
			LastReset     string `json:"last_reset"`
		} `json:"budgets"`
	}
	// x-bf-vk takes any virtual key; Authorization only takes sk-bf- ones.
	if err := getGateway(ctx, hc, root+"/api/governance/virtual-keys/quota", "x-bf-vk", key, &resp); err != nil {
		return usage.Reading{}, err
	}
	r := usage.Reading{Scope: "key", LimitReached: !resp.IsActive}
	for _, b := range resp.Budgets {
		if !b.MaxLimit.ok || b.MaxLimit.v <= 0 {
			continue
		}
		d := bifrostDurations[b.ResetDuration]
		w := usage.Window{Name: "budget (" + b.ResetDuration + ")", UsedPct: pct(b.CurrentUsage.v, b.MaxLimit.v), Duration: d}
		if last := parseTime(b.LastReset); !last.IsZero() && d > 0 {
			w.ResetsAt = last.Add(d)
		}
		r.Windows = append(r.Windows, w)
		// The monthly budget, or the first, is the spend against a cap.
		if !r.HasUsed || b.ResetDuration == "1M" {
			r.UsedUSD, r.HasUsed, r.LimitUSD = b.CurrentUsage.v, true, b.MaxLimit.v
		}
		if b.CurrentUsage.v >= b.MaxLimit.v {
			r.LimitReached = true
		}
	}
	return r, nil
}
