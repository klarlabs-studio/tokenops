package accounts

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// readerZAI registers the reader (readers_gen.go).
func readerZAI() usage.Reader { return ZAI{} }

// readerZhipuAI registers the reader (readers_gen.go).
func readerZhipuAI() usage.Reader { return ZhipuAI{} }

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

func (z ZAI) Read(ctx context.Context, key string) (usage.Reading, error) {
	return readZAIQuota(ctx, z.HTTP, base(z.BaseURL, "https://api.z.ai"), key, "z.ai")
}

// ZhipuAI reads the GLM Coding Plan sold on Zhipu AI's mainland-China
// platform. Its open.bigmodel.cn host serves the same quota endpoint, with
// the same request and answer, as z.ai's own ZCode client documents
// (zai-org/ZCode docs/bigmodel-coding-plan-usage-quota.md). Its keys are
// read only from its own endpoint, never taken from z.ai's.
type ZhipuAI struct {
	BaseURL string
	HTTP    *http.Client
}

func (ZhipuAI) Endpoint() string               { return "zhipuai" }
func (ZhipuAI) Provider() eventschema.Provider { return "zhipuai" }
func (ZhipuAI) Source() string                 { return "zhipuai-account" }

func (z ZhipuAI) baseURL() string { return base(z.BaseURL, "https://open.bigmodel.cn") }

func (z ZhipuAI) Read(ctx context.Context, key string) (usage.Reading, error) {
	return readZAIQuota(ctx, z.HTTP, z.baseURL(), key, "Zhipu AI")
}

// zaiUnits is the window length unit z.ai reports. Taken from community
// parsers; z.ai does not document it.
var zaiUnits = map[int]time.Duration{1: 24 * time.Hour, 3: time.Hour, 5: time.Minute, 6: 7 * 24 * time.Hour}

// zaiWindowTypes are the rows that are the coding plan's 5-hour and weekly
// windows: TOKENS_LIMIT on older plans, CREDIT_LIMIT on those z.ai has sold
// since 2026-07-30, with the same unit and number (zai-org/ZCode docs/
// coding-plan/zai-bigmodel-team-plan-symmetry.md). TIME_LIMIT, the monthly
// web-search and MCP tool allowance, is not usage of the model.
var zaiWindowTypes = map[string]bool{"TOKENS_LIMIT": true, "CREDIT_LIMIT": true}

type zaiLimit struct {
	Type          string `json:"type"`
	Unit          int    `json:"unit"`
	Number        int    `json:"number"`
	Percentage    number `json:"percentage"`
	Allowance     number `json:"usage"`        // the window's allowance
	Spent         number `json:"currentValue"` // what is spent of it
	NextResetTime int64  `json:"nextResetTime"`
}

// usedPct is the share spent: percentage when the row gives it, else
// currentValue of usage.
func (l zaiLimit) usedPct() (float64, bool) {
	if l.Percentage.ok {
		return l.Percentage.v, true
	}
	if l.Spent.ok && l.Allowance.ok && l.Allowance.v > 0 {
		return l.Spent.v / l.Allowance.v * 100, true
	}
	return 0, false
}

func readZAIQuota(ctx context.Context, hc *http.Client, host, key, vendor string) (usage.Reading, error) {
	var resp struct {
		Success bool   `json:"success"`
		Code    int    `json:"code"`
		Msg     string `json:"msg"`
		Data    struct {
			Limits []zaiLimit `json:"limits"`
		} `json:"data"`
	}
	if err := getJSONAuth(ctx, hc, host+"/api/monitor/usage/quota/limit", key, &resp); err != nil {
		return usage.Reading{}, err
	}
	if !resp.Success {
		// z.ai answers 200 with the refusal in the body: 1001 when no key
		// came, 1002 and up for a key it does not accept.
		if resp.Code >= 1000 && resp.Code < 1100 {
			return usage.Reading{}, fmt.Errorf("%w (%s %d)", usage.ErrAuth, vendor, resp.Code)
		}
		return usage.Reading{}, fmt.Errorf("accounts: %s quota: %d %s", vendor, resp.Code, resp.Msg)
	}
	r := usage.Reading{Scope: "key", Subscription: true}
	for _, l := range resp.Data.Limits {
		pct, ok := l.usedPct()
		if !zaiWindowTypes[l.Type] || !ok {
			continue
		}
		w := usage.Window{UsedPct: pct}
		if unit, ok := zaiUnits[l.Unit]; ok && l.Number > 0 {
			w.Duration = unit * time.Duration(l.Number)
		}
		w.Name = windowName(w.Duration)
		if l.NextResetTime > 0 {
			w.ResetsAt = time.UnixMilli(l.NextResetTime).UTC()
		}
		r.Windows = append(r.Windows, w)
	}
	if len(r.Windows) == 0 {
		return usage.Reading{}, zaiUnrecognised(vendor, resp.Data.Limits)
	}
	return r, nil
}

// zaiUnrecognised says why an answer held no plan window, naming the row
// types it did hold so a new one is visible in `vendor-usage status`.
func zaiUnrecognised(vendor string, limits []zaiLimit) error {
	if len(limits) == 0 {
		return fmt.Errorf("accounts: %s quota: unrecognised answer: no limits", vendor)
	}
	seen := map[string]bool{}
	for _, l := range limits {
		seen[l.Type] = true
	}
	types := make([]string, 0, len(seen))
	for t := range seen {
		types = append(types, t)
	}
	sort.Strings(types)
	return errors.New("accounts: " + vendor + " quota: unrecognised answer: no readable 5-hour or weekly window among " + strings.Join(types, ", "))
}
