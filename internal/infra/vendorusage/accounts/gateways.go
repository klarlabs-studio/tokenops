package accounts

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
)

// Gateways is every gateway whose budget the calling key can read.
// Portkey and Cloudflare AI Gateway are not here: neither lets the key in
// use read its own spend.
func Gateways() []usage.Gateway {
	return []usage.Gateway{ClawRouter{}, LiteLLM{}, Bifrost{}}
}

// probe GETs url with no key and returns the body of a 200, for
// recognising a gateway by its health route.
func probe(ctx context.Context, hc *http.Client, url string) ([]byte, bool) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, false
	}
	if hc == nil {
		hc = &http.Client{Timeout: 5 * time.Second}
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, false
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, false
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	return body, err == nil
}

// getGateway GETs a gateway route with the key in header.
func getGateway(ctx context.Context, hc *http.Client, url, header, value string, out any) error {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set(header, value)
	req.Header.Set("Accept", "application/json")
	if hc == nil {
		hc = &http.Client{Timeout: 20 * time.Second}
	}
	resp, err := hc.Do(req)
	if err != nil {
		return fmt.Errorf("accounts: GET %s: %w", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return fmt.Errorf("%w (%d on %s)", usage.ErrAuth, resp.StatusCode, url)
	case resp.StatusCode != http.StatusOK:
		return fmt.Errorf("accounts: GET %s: status %d", url, resp.StatusCode)
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("accounts: GET %s: %w", url, err)
	}
	return nil
}

// ClawRouter reads GET /v1/usage (openclaw/clawrouter docs/api-reference):
// the policy's budget for the calendar month, in micro-dollars.
type ClawRouter struct {
	// HTTP is the client; nil uses a default.
	HTTP *http.Client
}

func (ClawRouter) Name() string   { return "clawrouter" }
func (ClawRouter) Source() string { return "clawrouter-account" }

// clawRouterHost is the hosted service.
const clawRouterHost = "clawrouter.openclaw.ai"

func (g ClawRouter) Recognise(ctx context.Context, root string) bool {
	hc := g.HTTP
	if u, err := url.Parse(root); err == nil && u.Hostname() == clawRouterHost {
		return true
	}
	body, ok := probe(ctx, hc, root+"/v1/health")
	if !ok {
		return false
	}
	var h struct {
		Service string `json:"service"`
	}
	return json.Unmarshal(body, &h) == nil && h.Service == "clawrouter-edge"
}

func (g ClawRouter) Read(ctx context.Context, root, key string) (usage.Reading, error) {
	hc := g.HTTP
	var resp struct {
		Budget struct {
			Configured  bool   `json:"configured"`
			Ledger      string `json:"ledger"`
			LimitMicros number `json:"limitMicros"`
			SpentMicros number `json:"spentMicros"`
		} `json:"budget"`
	}
	if err := getGateway(ctx, hc, root+"/v1/usage", "Authorization", "Bearer "+key, &resp); err != nil {
		return usage.Reading{}, err
	}
	b := resp.Budget
	r := usage.Reading{Scope: "key", LimitReached: b.Ledger == "blocked"}
	if b.SpentMicros.ok {
		r.UsedUSD, r.HasUsed = b.SpentMicros.v/1e6, true
	}
	if b.Configured && b.LimitMicros.ok && b.LimitMicros.v > 0 {
		r.LimitUSD = b.LimitMicros.v / 1e6
	}
	return r, nil
}

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

// The readers and gateways satisfy the domain's ports.
var (
	_ usage.Reader  = OpenRouter{}
	_ usage.Gateway = ClawRouter{}
	_ usage.Gateway = LiteLLM{}
	_ usage.Gateway = Bifrost{}
)
