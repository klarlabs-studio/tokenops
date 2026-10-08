package accounts

import (
	"context"
	"fmt"
	"net/http"
	"time"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// readerZAI registers the reader (readers_gen.go).
func readerZAI() usage.Reader { return ZAI{} }

// ZAI reads the GLM Coding Plan's windows from GET
// /api/monitor/usage/quota/limit. The endpoint is not in z.ai's API docs;
// it is what z.ai's own usage plugin (zai-org/zai-coding-plugins) calls,
// with the plan's key sent raw rather than as a bearer token.
type ZAI struct {
	BaseURL string
	HTTP    *http.Client
}

func (ZAI) Endpoint() string               { return "zai" }
func (ZAI) Provider() eventschema.Provider { return "zai" }
func (ZAI) Source() string                 { return "zai-account" }

// zaiUnits is the window length unit z.ai reports. Taken from community
// parsers; z.ai does not document it.
var zaiUnits = map[int]time.Duration{1: 24 * time.Hour, 3: time.Hour, 5: time.Minute, 6: 7 * 24 * time.Hour}

func (z ZAI) Read(ctx context.Context, key string) (usage.Reading, error) {
	var resp struct {
		Success bool   `json:"success"`
		Code    int    `json:"code"`
		Msg     string `json:"msg"`
		Data    struct {
			Limits []struct {
				Type          string `json:"type"`
				Unit          int    `json:"unit"`
				Number        int    `json:"number"`
				Percentage    number `json:"percentage"`
				NextResetTime int64  `json:"nextResetTime"`
			} `json:"limits"`
		} `json:"data"`
	}
	if err := getJSONAuth(ctx, z.HTTP, base(z.BaseURL, "https://api.z.ai")+"/api/monitor/usage/quota/limit", key, &resp); err != nil {
		return usage.Reading{}, err
	}
	if !resp.Success {
		// z.ai answers 200 with the refusal in the body: 1001 when no key
		// came, 1002 and up for a key it does not accept.
		if resp.Code >= 1000 && resp.Code < 1100 {
			return usage.Reading{}, fmt.Errorf("%w (z.ai %d)", usage.ErrAuth, resp.Code)
		}
		return usage.Reading{}, fmt.Errorf("accounts: z.ai quota: %d %s", resp.Code, resp.Msg)
	}
	r := usage.Reading{Scope: "key", Subscription: true}
	for _, l := range resp.Data.Limits {
		// TOKENS_LIMIT is the coding plan's 5-hour and weekly windows;
		// the MCP tool quota is not usage of the model.
		if l.Type != "TOKENS_LIMIT" || !l.Percentage.ok {
			continue
		}
		w := usage.Window{UsedPct: l.Percentage.v}
		if unit, ok := zaiUnits[l.Unit]; ok && l.Number > 0 {
			w.Duration = unit * time.Duration(l.Number)
		}
		w.Name = windowName(w.Duration)
		if l.NextResetTime > 0 {
			w.ResetsAt = time.UnixMilli(l.NextResetTime).UTC()
		}
		r.Windows = append(r.Windows, w)
	}
	return r, nil
}
