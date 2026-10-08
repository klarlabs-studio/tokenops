package accounts

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// readerOpencodeGo registers the reader (readers_gen.go).
func readerOpencodeGo() usage.Reader { return OpencodeGo{} }

// OpencodeGo reads opencode Go's windows from GET /zen/go/v1/usage with the
// opencode API key, the endpoint CodexBar's OpenCodeGoUsageFetcher calls
// (fetchAPIUsage); opencode does not document it. Each window reports the
// share used as a percentage (0–100) and when it resets.
type OpencodeGo struct {
	BaseURL string
	HTTP    *http.Client
}

func (OpencodeGo) Endpoint() string               { return "opencode-go" }
func (OpencodeGo) Provider() eventschema.Provider { return "opencode-go" }
func (OpencodeGo) Source() string                 { return "opencode-go-account" }

// errNoGoWindows is an answer without the rolling window every Go
// subscription has: no subscription, or a shape this version cannot read.
var errNoGoWindows = errors.New("accounts: opencode Go answered without usage windows")

func (o OpencodeGo) Read(ctx context.Context, key string) (usage.Reading, error) {
	var resp struct {
		Usage map[string]json.RawMessage `json:"usage"`
	}
	if err := getJSON(ctx, o.HTTP, base(o.BaseURL, "https://opencode.ai")+"/zen/go/v1/usage", key, &resp); err != nil {
		return usage.Reading{}, err
	}
	now := time.Now().UTC()
	r := usage.Reading{Scope: "account", Subscription: true}
	for _, w := range []struct {
		key, name string
		d         time.Duration
	}{
		{"rolling", "5h", 5 * time.Hour},
		{"weekly", "week", 7 * 24 * time.Hour},
		{"monthly", "month", 30 * 24 * time.Hour},
	} {
		win, ok := opencodeGoWindow(resp.Usage[w.key], now)
		if !ok {
			if w.key == "rolling" {
				return usage.Reading{}, errNoGoWindows
			}
			continue
		}
		win.Name, win.Duration = w.name, w.d
		r.Windows = append(r.Windows, win)
	}
	return r, nil
}

// opencodeGoWindow reads one window as CodexBar's parseWindow does for the
// API: a direct percentage, or used against a limit; the reset as seconds
// from now or as a time.
func opencodeGoWindow(raw json.RawMessage, now time.Time) (usage.Window, bool) {
	var m map[string]json.RawMessage
	if len(raw) == 0 || json.Unmarshal(raw, &m) != nil {
		return usage.Window{}, false
	}
	pct, ok := firstJSONNumber(m, "usagePercent", "usedPercent", "percentUsed", "percent", "usage_percent", "used_percent", "utilization")
	if !ok {
		used, okU := firstJSONNumber(m, "used", "usage", "consumed")
		limit, okL := firstJSONNumber(m, "limit", "total", "quota", "max")
		if !okU || !okL || limit <= 0 {
			return usage.Window{}, false
		}
		pct = used / limit * 100
	}
	w := usage.Window{UsedPct: clampPct(pct)}
	if sec, ok := firstJSONNumber(m, "resetInSec", "resetInSeconds", "resetSeconds", "reset_in_sec", "resetsInSec"); ok && sec > 0 {
		w.ResetsAt = now.Add(time.Duration(sec) * time.Second).Truncate(time.Second)
	} else {
		for _, k := range []string{"resetsAt", "resetAt", "resets_at", "reset_at"} {
			var s string
			if json.Unmarshal(m[k], &s) == nil {
				if t := parseTime(s); !t.IsZero() {
					w.ResetsAt = t
					break
				}
			}
		}
	}
	return w, true
}

// firstJSONNumber is the first of keys holding a number or a numeric string.
func firstJSONNumber(m map[string]json.RawMessage, keys ...string) (float64, bool) {
	for _, k := range keys {
		var n number
		if raw, ok := m[k]; ok && json.Unmarshal(raw, &n) == nil && n.ok {
			return n.v, true
		}
	}
	return 0, false
}
