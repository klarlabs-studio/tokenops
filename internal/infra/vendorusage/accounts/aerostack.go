package accounts

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// readerAerostack registers the reader (readers_gen.go).
func readerAerostack() usage.Reader { return Aerostack{} }

// Aerostack reads GET api.aerostack.dev/api/billing/usage
// (docs.aerostack.dev/billing), ported from CodexBar (Sources/CodexBarCore/
// Resources/Plugins/aerostack.ts, Providers/Aerostack, docs/aerostack.md):
// the account's AI tokens used this month against its allowance, with the
// account JWT (not an Agent Endpoint key). The answer names the period
// ("2026-03") but no reset time or zone, so none is claimed. Use above the
// allowance is not a block: Aerostack documents a grace buffer.
type Aerostack struct {
	BaseURL string
	HTTP    *http.Client
}

func (Aerostack) Endpoint() string               { return "aerostack" }
func (Aerostack) Provider() eventschema.Provider { return "aerostack" }
func (Aerostack) Source() string                 { return "aerostack-account" }

func (a Aerostack) Read(ctx context.Context, key string) (usage.Reading, error) {
	var resp struct {
		Usage *struct {
			AITokens *struct {
				Used  json.RawMessage `json:"used"`
				Limit json.RawMessage `json:"limit"`
			} `json:"ai_tokens"`
		} `json:"usage"`
	}
	if err := getJSON(ctx, a.HTTP, base(a.BaseURL, "https://api.aerostack.dev")+"/api/billing/usage", key, &resp); err != nil {
		return usage.Reading{}, err
	}
	if resp.Usage == nil || resp.Usage.AITokens == nil {
		return usage.Reading{}, errors.New("accounts: aerostack usage: unrecognised answer")
	}
	used, hasUsed, ok1 := optionalCount(resp.Usage.AITokens.Used)
	limit, hasLimit, ok2 := optionalCount(resp.Usage.AITokens.Limit)
	if !ok1 || !ok2 || (!hasUsed && !hasLimit) {
		return usage.Reading{}, errors.New("accounts: aerostack usage: unrecognised answer")
	}
	r := usage.Reading{Scope: "account", Subscription: true}
	switch {
	case hasUsed && hasLimit && limit > 0:
		r.Windows = []usage.Window{{Name: "month (AI tokens)", UsedPct: clampPct(pct(used, limit))}}
	case hasUsed:
		r.Counts = []usage.Count{{Name: "AI tokens", Used: used}}
	}
	return r, nil
}

// optionalCount reads a non-negative whole number that may be absent or
// null; ok is false for anything else.
func optionalCount(raw json.RawMessage) (v float64, present, ok bool) {
	if len(raw) == 0 || string(raw) == "null" {
		return 0, false, true
	}
	var n int64
	if json.Unmarshal(raw, &n) != nil || n < 0 {
		return 0, false, false
	}
	return float64(n), true, true
}
