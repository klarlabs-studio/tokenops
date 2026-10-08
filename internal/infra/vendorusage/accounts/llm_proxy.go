package accounts

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"time"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
)

// gatewayLLMProxy registers the gateway (gateways_gen.go).
func gatewayLLMProxy() usage.Gateway { return LLMProxy{} }

// LLMProxy reads GET /v1/quota-stats on an LLM-API-Key-Proxy
// (Mirrowel/LLM-API-Key-Proxy), which rotates the operator's own provider
// credentials, as CodexBar's LLM Proxy provider does. What it reports is
// the quota left on the credentials it pools, per provider and quota
// group; TokenOps takes the tightest of them, as CodexBar does. Its
// approx_cost is a running total with no period, so it is not read as
// spend.
type LLMProxy struct {
	// HTTP is the client; nil uses a default.
	HTTP *http.Client
	// Now is the clock; nil uses time.Now.
	Now func() time.Time
}

func (LLMProxy) Name() string   { return "llm-proxy" }
func (LLMProxy) Source() string { return "llm-proxy-account" }

// Recognise asks GET /, which the proxy answers without a key
// (src/proxy_app/main.py, read_root).
func (g LLMProxy) Recognise(ctx context.Context, root string) bool {
	body, ok := probe(ctx, g.HTTP, root+"/")
	return ok && bytes.Contains(body, []byte("API Key Proxy is running"))
}

// llmProxyGroup is one quota group, in either shape the proxy has
// answered with: a group-level remaining_percent and reset_time (what
// CodexBar parses), or per-window remaining_pct (the rotator library's
// UsageManager).
type llmProxyGroup struct {
	RemainingPercent number `json:"remaining_percent"`
	ResetTime        string `json:"reset_time"`
	Windows          map[string]struct {
		RemainingPct number `json:"remaining_pct"`
	} `json:"windows"`
}

func (g LLMProxy) Read(ctx context.Context, root, key string) (usage.Reading, error) {
	var resp struct {
		Providers map[string]struct {
			QuotaGroups json.RawMessage `json:"quota_groups"`
		} `json:"providers"`
	}
	if err := getGateway(ctx, g.HTTP, root+"/v1/quota-stats", "Authorization", "Bearer "+key, &resp); err != nil {
		return usage.Reading{}, err
	}
	now := time.Now
	if g.Now != nil {
		now = g.Now
	}
	var (
		tightest float64
		found    bool
		reset    time.Time
	)
	for _, p := range resp.Providers {
		for _, q := range llmProxyGroups(p.QuotaGroups) {
			left := []float64{}
			if q.RemainingPercent.ok {
				left = append(left, q.RemainingPercent.v)
			}
			for _, w := range q.Windows {
				if w.RemainingPct.ok {
					left = append(left, w.RemainingPct.v)
				}
			}
			for _, l := range left {
				if used := clampPct(100 - l); !found || used > tightest {
					tightest, found = used, true
				}
			}
			// The soonest reset still ahead, as CodexBar shows it.
			if t := parseTime(q.ResetTime); t.After(now()) && (reset.IsZero() || t.Before(reset)) {
				reset = t
			}
		}
	}
	r := usage.Reading{Scope: "gateway"}
	if found {
		// One exhausted group does not block the others' requests, so
		// this is a window, not a limit reached.
		r.Windows = []usage.Window{{Name: "quota", UsedPct: tightest, ResetsAt: reset}}
	}
	return r, nil
}

// llmProxyGroups reads quota_groups as an array or a keyed object; a
// malformed one is absent, as in CodexBar.
func llmProxyGroups(raw json.RawMessage) []llmProxyGroup {
	var list []llmProxyGroup
	if json.Unmarshal(raw, &list) == nil {
		return list
	}
	var keyed map[string]llmProxyGroup
	if json.Unmarshal(raw, &keyed) != nil {
		return nil
	}
	for _, q := range keyed {
		list = append(list, q)
	}
	return list
}
