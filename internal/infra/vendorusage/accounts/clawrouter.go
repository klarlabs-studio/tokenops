package accounts

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
)

// gatewayClawRouter registers the gateway (gateways_gen.go).
func gatewayClawRouter() usage.Gateway { return ClawRouter{} }

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
