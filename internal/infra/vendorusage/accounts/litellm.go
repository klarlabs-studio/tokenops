package accounts

import (
	"bytes"
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
)

// gatewayLiteLLM registers the gateway (gateways_gen.go).
func gatewayLiteLLM() usage.Gateway { return LiteLLM{} }

// LiteLLM reads GET /key/info, which a virtual key may call about itself
// (BerriAI/litellm key_management_endpoints): its spend in the current
// budget window, the budget, and when it resets.
type LiteLLM struct {
	// HTTP is the client; nil uses a default.
	HTTP *http.Client
}

func (LiteLLM) Name() string   { return "litellm" }
func (LiteLLM) Source() string { return "litellm-account" }

func (g LiteLLM) Recognise(ctx context.Context, root string) bool {
	hc := g.HTTP
	body, ok := probe(ctx, hc, root+"/health/liveliness")
	return ok && bytes.Contains(body, []byte("I'm alive"))
}

func (g LiteLLM) Read(ctx context.Context, root, key string) (usage.Reading, error) {
	hc := g.HTTP
	var resp struct {
		Info struct {
			Spend          number `json:"spend"`
			MaxBudget      number `json:"max_budget"`
			BudgetDuration string `json:"budget_duration"`
			BudgetResetAt  string `json:"budget_reset_at"`
			Status         string `json:"status"`
			Blocked        bool   `json:"blocked"`
		} `json:"info"`
	}
	if err := getGateway(ctx, hc, root+"/key/info", "Authorization", "Bearer "+key, &resp); err != nil {
		return usage.Reading{}, err
	}
	in := resp.Info
	r := usage.Reading{Scope: "key", LimitReached: in.Blocked || (in.Status != "" && in.Status != "active")}
	if in.Spend.ok {
		r.UsedUSD, r.HasUsed = in.Spend.v, true
	}
	if in.MaxBudget.ok && in.MaxBudget.v > 0 {
		r.LimitUSD = in.MaxBudget.v
		r.LimitReached = r.LimitReached || r.UsedUSD >= r.LimitUSD
		// A budget that resets is a window: the share of it spent.
		if d, ok := parseLiteLLMDuration(in.BudgetDuration); ok {
			r.Windows = []usage.Window{{Name: "budget (" + in.BudgetDuration + ")", UsedPct: pct(r.UsedUSD, r.LimitUSD),
				Duration: d, ResetsAt: parseTime(in.BudgetResetAt)}}
		}
	}
	return r, nil
}

// parseLiteLLMDuration reads LiteLLM's budget durations: 30s, 30m, 1h, 1d,
// 30d, 1mo.
func parseLiteLLMDuration(s string) (time.Duration, bool) {
	s = strings.TrimSpace(s)
	for suffix, unit := range map[string]time.Duration{"mo": 30 * 24 * time.Hour, "d": 24 * time.Hour, "h": time.Hour, "m": time.Minute, "s": time.Second} {
		if n, ok := strings.CutSuffix(s, suffix); ok {
			if v, err := strconv.Atoi(n); err == nil && v > 0 {
				return time.Duration(v) * unit, true
			}
		}
	}
	return 0, false
}
