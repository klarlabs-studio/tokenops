package accounts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// readerLangdock registers the reader (readers_gen.go).
func readerLangdock() usage.Reader { return Langdock{} }

// Langdock reads the personal included-usage limits (a 5-hour session and
// a week) from the Langdock app's own tRPC call,
// usageSettings.getPersonalUsage, with its browser session, as CodexBar's
// Langdock provider does (Sources/CodexBarCore/Resources/Plugins/langdock.ts,
// docs/langdock.md). An account without included limits reads empty. The
// session share is read only when session limits are on.
type Langdock struct {
	BaseURL string
	HTTP    *http.Client
}

func (Langdock) Endpoint() string               { return "langdock" }
func (Langdock) Provider() eventschema.Provider { return "langdock" }
func (Langdock) Source() string                 { return "langdock-web" }

func (l Langdock) Read(ctx context.Context, cookie string) (usage.Reading, error) {
	cookie = cookieHeader(cookie)
	if !isSession(cookie) || cookieValue(cookie, "auth_token") == "" {
		return usage.Reading{}, fmt.Errorf("%w (Langdock is read with the app's auth_token cookie)", usage.ErrAuth)
	}
	input := url.QueryEscape(`{"0":{"json":null,"meta":{"values":["undefined"],"v":1}}}`)
	u := base(l.BaseURL, "https://app.langdock.com") + "/api/trpc/usageSettings.getPersonalUsage?batch=1&input=" + input
	body, err := doWeb(ctx, l.HTTP, http.MethodGet, u, http.Header{
		"Cookie": {cookie}, "Accept": {"application/json"}, "Referer": {"https://app.langdock.com/settings/account/usage"},
	}, nil)
	if err != nil {
		return usage.Reading{}, err
	}
	var batch []struct {
		Result *struct {
			Data struct {
				JSON json.RawMessage `json:"json"`
			} `json:"data"`
		} `json:"result"`
		Error *struct {
			JSON struct {
				Data struct {
					Code *string `json:"code"`
				} `json:"data"`
			} `json:"json"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &batch) != nil || len(batch) != 1 {
		return usage.Reading{}, errors.New("accounts: GET app.langdock.com/api/trpc: unexpected batch shape")
	}
	if e := batch[0].Error; e != nil {
		if e.JSON.Data.Code == nil {
			return usage.Reading{}, errors.New("accounts: GET app.langdock.com/api/trpc: unexpected error shape")
		}
		code := strings.ToUpper(*e.JSON.Data.Code)
		if code == "UNAUTHORIZED" || code == "FORBIDDEN" {
			return usage.Reading{}, fmt.Errorf("%w (tRPC %s on app.langdock.com/api/trpc)", usage.ErrAuth, code)
		}
		return usage.Reading{}, fmt.Errorf("accounts: GET app.langdock.com/api/trpc: tRPC error %s", code)
	}
	if batch[0].Result == nil {
		return usage.Reading{}, errors.New("accounts: GET app.langdock.com/api/trpc: no result")
	}
	return langdockReading(batch[0].Result.Data.JSON)
}

func langdockReading(raw json.RawMessage) (usage.Reading, error) {
	bad := errors.New("accounts: GET app.langdock.com/api/trpc: unexpected usage shape")
	var p struct {
		HasLimits *json.RawMessage `json:"hasIncludedUsageLimits"`
		Plan      *struct {
			SessionEnabled json.RawMessage `json:"sessionUsageLimitsEnabled"`
			SessionPct     json.RawMessage `json:"sessionUsagePercent"`
			SessionReset   json.RawMessage `json:"sessionResetsAt"`
			WeeklyPct      json.RawMessage `json:"weeklyUsagePercent"`
			WeeklyReset    json.RawMessage `json:"weeklyResetsAt"`
		} `json:"planUsage"`
	}
	if json.Unmarshal(raw, &p) != nil {
		return usage.Reading{}, bad
	}
	if p.HasLimits != nil {
		has, ok := museBool(*p.HasLimits)
		if !ok || string(*p.HasLimits) == "null" {
			return usage.Reading{}, bad
		}
		if !has {
			return usage.Reading{Scope: "account"}, nil
		}
	}
	if p.Plan == nil {
		return usage.Reading{Scope: "account"}, nil
	}
	var sessionOn bool
	if json.Unmarshal(p.Plan.SessionEnabled, &sessionOn) != nil {
		return usage.Reading{}, bad
	}
	r := usage.Reading{Scope: "account", Subscription: true}
	if sessionOn {
		w, ok := langdockWindow(p.Plan.SessionPct, p.Plan.SessionReset, 5*time.Hour)
		if !ok {
			return usage.Reading{}, bad
		}
		r.Windows = append(r.Windows, w)
	}
	w, ok := langdockWindow(p.Plan.WeeklyPct, p.Plan.WeeklyReset, 7*24*time.Hour)
	if !ok {
		return usage.Reading{}, bad
	}
	r.Windows = append(r.Windows, w)
	return r, nil
}

// langdockWindow reads a share (a finite number; Langdock lets usage run
// over 100, which is held at 100 as every window is) and an ISO reset,
// null when unknown.
func langdockWindow(pctRaw, resetRaw json.RawMessage, d time.Duration) (usage.Window, bool) {
	used, ok := finiteNumber(pctRaw)
	if !ok {
		return usage.Window{}, false
	}
	w := usage.Window{Name: windowName(d), UsedPct: clampPct(used), Duration: d}
	if len(resetRaw) == 0 || string(resetRaw) == "null" {
		return w, true
	}
	var s string
	if json.Unmarshal(resetRaw, &s) != nil {
		return usage.Window{}, false
	}
	if w.ResetsAt = parseTime(s); w.ResetsAt.IsZero() {
		return usage.Window{}, false
	}
	return w, true
}
