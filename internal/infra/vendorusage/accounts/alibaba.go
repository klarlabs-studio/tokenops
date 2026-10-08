package accounts

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// The Alibaba Cloud Coding Plan's 5-hour, weekly and monthly quotas, read
// from the Model Studio (international) or Bailian (mainland) console the
// way CodexBar's Alibaba provider does
// (Sources/CodexBarCore/Providers/Alibaba/AlibabaCodingPlanUsageFetcher.swift):
// with the Coding Plan API key where the console accepts one, or with the
// console's browser session. Each region is tried, international first,
// when the other refuses.

// readerAlibaba registers the API-key reader (readers_gen.go).
func readerAlibaba() usage.Reader { return Alibaba{} }

// readerAlibabaWeb registers the browser-session reader (readers_gen.go).
func readerAlibabaWeb() usage.Reader { return AlibabaWeb{} }

// alibabaRegion is one console the Coding Plan is read from.
type alibabaRegion struct {
	gateway, rpc, regionID, commodity, consoleDomain, consoleSite, action, dashboard string
}

var alibabaRegions = []alibabaRegion{
	{
		gateway: "https://modelstudio.console.alibabacloud.com", rpc: "https://bailian-singapore-cs.alibabacloud.com",
		regionID: "ap-southeast-1", commodity: "sfm_codingplan_public_intl",
		consoleDomain: "modelstudio.console.alibabacloud.com", consoleSite: "MODELSTUDIO_ALIBABACLOUD", action: "IntlBroadScopeAspnGateway",
		dashboard: "https://modelstudio.console.alibabacloud.com/ap-southeast-1/?tab=coding-plan#/efm/coding_plan",
	},
	{
		gateway: "https://bailian.console.aliyun.com", rpc: "https://bailian-cs.console.aliyun.com",
		regionID: "cn-beijing", commodity: "sfm_codingplan_public_cn",
		consoleDomain: "bailian.console.aliyun.com", consoleSite: "BAILIAN_ALIYUN", action: "BroadScopeAspnGateway",
		dashboard: "https://bailian.console.aliyun.com/cn-beijing/?tab=model#/efm/coding_plan",
	},
}

const alibabaQuotaAPI = "zeldaEasy.broadscope-bailian.codingPlan.queryCodingPlanInstanceInfoV2"

// Alibaba reads the Coding Plan with its API key (sk-sp-…), which opencode
// and the environment hold. Some mainland accounts' consoles answer a key
// with a sign-in demand; the browser-session reader covers those.
type Alibaba struct {
	// BaseURLs replace the regions' console hosts in tests, in order.
	BaseURLs []string
	HTTP     *http.Client
}

func (Alibaba) Endpoint() string               { return "alibaba" }
func (Alibaba) Provider() eventschema.Provider { return "alibaba" }
func (Alibaba) Source() string                 { return "alibaba-account" }
func (Alibaba) KeyOnly() bool                  { return true }

func (a Alibaba) Read(ctx context.Context, key string) (usage.Reading, error) {
	if isSession(key) {
		return usage.Reading{}, usage.ErrSkip
	}
	return alibabaEachRegion(a.BaseURLs, func(r alibabaRegion) (usage.Reading, error) {
		q := url.Values{"action": {alibabaQuotaAPI}, "product": {"broadscope-bailian"}, "api": {"queryCodingPlanInstanceInfoV2"}, "currentRegionId": {r.regionID}}
		body := fmt.Sprintf(`{"queryCodingPlanInstanceInfoRequest":{"commodityCode":%q}}`, r.commodity)
		data, err := doWeb(ctx, a.HTTP, http.MethodPost, r.gateway+"/data/api.json?"+q.Encode(), http.Header{
			"Authorization": {"Bearer " + key}, "X-Api-Key": {key}, "X-Dashscope-Api-Key": {key},
			"Content-Type": {"application/json"}, "Accept": {"application/json"},
			"Origin": {r.gateway}, "Referer": {r.dashboard},
		}, []byte(body))
		if err != nil {
			return usage.Reading{}, err
		}
		return parseAlibabaCoding(data, true)
	})
}

// AlibabaWeb reads the Coding Plan with the console's browser session.
type AlibabaWeb struct {
	BaseURLs []string
	HTTP     *http.Client
}

func (AlibabaWeb) Endpoint() string               { return "alibaba" }
func (AlibabaWeb) Provider() eventschema.Provider { return "alibaba" }
func (AlibabaWeb) Source() string                 { return "alibaba-web" }

func (a AlibabaWeb) Read(ctx context.Context, cookie string) (usage.Reading, error) {
	if !isSession(cookie) {
		return usage.Reading{}, usage.ErrSkip
	}
	return alibabaEachRegion(a.BaseURLs, func(r alibabaRegion) (usage.Reading, error) {
		gateway, rpc := r.gateway, r.rpc
		token := consoleSecToken(ctx, a.HTTP, gateway, r.dashboard, cookie)
		if token == "" {
			return usage.Reading{}, fmt.Errorf("%w (no console security token on %s: the session is signed out)", usage.ErrAuth, hostPath(gateway))
		}
		cornerstone := map[string]any{
			"feTraceId": uuid.NewString(), "feURL": r.dashboard, "protocol": "V2", "console": "ONE_CONSOLE",
			"productCode": "p_efm", "domain": r.consoleDomain, "consoleSite": r.consoleSite,
			"userNickName": "", "userPrincipalName": "", "xsp_lang": "en-US",
		}
		if cna := cookieValue(cookie, "cna"); cna != "" {
			cornerstone["X-Anonymous-Id"] = cna
		}
		params := mustJSON(map[string]any{"Api": alibabaQuotaAPI, "V": "1.0", "Data": map[string]any{
			"queryCodingPlanInstanceInfoRequest": map[string]any{"commodityCode": r.commodity, "onlyLatestOne": true},
			"cornerstoneParam":                   cornerstone,
		}})
		q := url.Values{"action": {r.action}, "product": {"sfm_bailian"}, "api": {alibabaQuotaAPI}, "_v": {"undefined"}}
		data, err := doWeb(ctx, a.HTTP, http.MethodPost, rpc+"/data/api.json?"+q.Encode(), consoleHeader(cookie, gateway, r.dashboard),
			formBody([][2]string{{"params", params}, {"region", r.regionID}, {"sec_token", token}}))
		if err != nil {
			return usage.Reading{}, err
		}
		return parseAlibabaCoding(data, false)
	})
}

// alibabaEachRegion reads the international console, then the mainland
// one when the first refuses or has no plan. bases replace the regions'
// hosts in tests (both the gateway and the RPC host).
func alibabaEachRegion(bases []string, read func(alibabaRegion) (usage.Reading, error)) (usage.Reading, error) {
	var err error
	for i, r := range alibabaRegions {
		if i < len(bases) && bases[i] != "" {
			r.gateway, r.rpc = strings.TrimRight(bases[i], "/"), strings.TrimRight(bases[i], "/")
		}
		var got usage.Reading
		if got, err = read(r); err == nil || !errors.Is(err, errOtherRegion) && !errors.Is(err, usage.ErrAuth) {
			return got, err
		}
	}
	return usage.Reading{}, err
}

// errOtherRegion is a console that answered without this account's plan.
var errOtherRegion = errors.New("no coding plan on this console")

// consoleHeader is the header of a OneConsole gateway call.
func consoleHeader(cookie, origin, referer string) http.Header {
	h := http.Header{
		"Cookie": {cookie}, "Content-Type": {"application/x-www-form-urlencoded"}, "Accept": {"application/json, text/plain, */*"},
		"X-Requested-With": {"XMLHttpRequest"}, "Origin": {origin}, "Referer": {referer},
	}
	csrf := cookieValue(cookie, "login_aliyunid_csrf")
	if csrf == "" {
		csrf = cookieValue(cookie, "csrf")
	}
	if csrf != "" {
		h["X-Xsrf-Token"], h["X-Csrf-Token"] = []string{csrf}, []string{csrf}
	}
	return h
}

// consoleSecToken is the console's security token: from its page, then
// its user-info call, then the sec_token cookie. "" when none answers.
func consoleSecToken(ctx context.Context, hc *http.Client, gateway, dashboard, cookie string) string {
	page := gateway + dashboardPath(dashboard)
	if body, err := doWeb(ctx, hc, http.MethodGet, page, http.Header{
		"Cookie": {cookie}, "Accept": {"text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8"},
		"Referer": {gateway + "/"}, "Sec-Fetch-Site": {"same-origin"}, "Sec-Fetch-Mode": {"navigate"}, "Sec-Fetch-Dest": {"document"},
	}, nil); err == nil {
		if t := secTokenIn(body); t != "" {
			return t
		}
	}
	if body, err := doWeb(ctx, hc, http.MethodGet, gateway+"/tool/user/info.json", http.Header{
		"Cookie": {cookie}, "Accept": {"application/json, text/plain, */*"}, "Referer": {gateway + "/"},
	}, nil); err == nil {
		if v, ok := decodeConsole(body); ok {
			if t := deepString(v, "secToken", "sec_token"); t != "" {
				return t
			}
		}
	}
	return cookieValue(cookie, "sec_token")
}

// dashboardPath is a dashboard URL's path and query, without its host.
func dashboardPath(dashboard string) string {
	u, err := url.Parse(dashboard)
	if err != nil {
		return "/"
	}
	out := u.EscapedPath()
	if u.RawQuery != "" {
		out += "?" + u.RawQuery
	}
	return out
}

// parseAlibabaCoding maps the console's answer: a refusal or sign-in demand
// is usage.ErrAuth (with a key, the console wanting a sign-in means the key
// cannot read quota there, which is an ordinary error), the active plan's
// three windows as used/total.
func parseAlibabaCoding(body []byte, keyMode bool) (usage.Reading, error) {
	v, ok := decodeConsole(body)
	if !ok {
		if looksLikeSignIn(body) {
			return usage.Reading{}, fmt.Errorf("%w (the console asked to sign in)", usage.ErrAuth)
		}
		return usage.Reading{}, errors.New("accounts: alibaba: the answer is not JSON")
	}
	if code, ok := deepNumber(v, "statusCode", "status_code", "code"); ok && code != 0 && code != 200 {
		msg := strings.ToLower(deepString(v, "statusMessage", "status_msg", "message", "msg"))
		if code == 401 || code == 403 || strings.Contains(msg, "api key") || strings.Contains(msg, "unauthorized") {
			return usage.Reading{}, fmt.Errorf("%w (code %.0f)", usage.ErrAuth, code)
		}
		return usage.Reading{}, fmt.Errorf("accounts: alibaba: code %.0f", code)
	}
	code := strings.ToLower(deepString(v, "code", "status", "statusCode"))
	msg := strings.ToLower(deepString(v, "message", "msg", "statusMessage"))
	if strings.Contains(code, "login") || strings.Contains(msg, "log in") || strings.Contains(msg, "login") {
		if keyMode {
			return usage.Reading{}, errors.New("accounts: alibaba: this console reads the coding plan only with a browser session (`tokenops vendor-usage setup alibaba`)")
		}
		return usage.Reading{}, fmt.Errorf("%w (the console asked to sign in)", usage.ErrAuth)
	}
	instance := alibabaActiveInstance(v)
	var quota map[string]any
	if instance != nil {
		quota, _ = instance["codingPlanQuotaInfo"].(map[string]any)
	}
	if quota == nil {
		if q, ok := objectWith(v, "codingPlanQuotaInfo")["codingPlanQuotaInfo"].(map[string]any); ok {
			quota = q
		}
	}
	if quota == nil {
		quota = objectWith(v, "per5HourUsedQuota", "per5HourTotalQuota", "perWeekUsedQuota", "perWeekTotalQuota", "perBillMonthUsedQuota", "perBillMonthTotalQuota")
	}
	r := usage.Reading{Scope: "account", Subscription: true}
	for _, w := range []struct {
		name             string
		d                time.Duration
		used, total, rst []string
	}{
		{"5h", 5 * time.Hour, []string{"per5HourUsedQuota", "perFiveHourUsedQuota"}, []string{"per5HourTotalQuota", "perFiveHourTotalQuota"}, []string{"per5HourQuotaNextRefreshTime", "perFiveHourQuotaNextRefreshTime"}},
		{"week", 7 * 24 * time.Hour, []string{"perWeekUsedQuota"}, []string{"perWeekTotalQuota"}, []string{"perWeekQuotaNextRefreshTime"}},
		{"month", 30 * 24 * time.Hour, []string{"perBillMonthUsedQuota", "perMonthUsedQuota"}, []string{"perBillMonthTotalQuota", "perMonthTotalQuota"}, []string{"perBillMonthQuotaNextRefreshTime", "perMonthQuotaNextRefreshTime"}},
	} {
		if quota == nil {
			break
		}
		total, ok := numOf(field(quota, w.total...))
		if !ok || total <= 0 {
			continue
		}
		used, _ := numOf(field(quota, w.used...))
		r.Windows = append(r.Windows, usage.Window{Name: w.name, UsedPct: clampPct(pct(used, total)), Duration: w.d, ResetsAt: timeOf(field(quota, w.rst...))})
	}
	if len(r.Windows) > 0 {
		return r, nil
	}
	// A plan the console shows as active but without counters has no share
	// to report; nothing at all is no plan on this console.
	if instance != nil && alibabaActiveScore(instance) > 0 {
		return r, nil
	}
	return usage.Reading{}, fmt.Errorf("accounts: alibaba: %w", errOtherRegion)
}

// alibabaActiveInstance is the plan instance with the strongest sign of
// being active, or the first.
func alibabaActiveInstance(v any) map[string]any {
	infos, _ := objectWith(v, "codingPlanInstanceInfos")["codingPlanInstanceInfos"].([]any)
	var first, best map[string]any
	bestScore := -2
	for _, x := range infos {
		m, ok := x.(map[string]any)
		if !ok {
			continue
		}
		if first == nil {
			first = m
		}
		if s := alibabaActiveScore(m); s > bestScore {
			best, bestScore = m, s
		}
	}
	if bestScore > 0 {
		return best
	}
	return first
}

func alibabaActiveScore(m map[string]any) int {
	switch strings.ToUpper(strOf(field(m, "status", "instanceStatus"))) {
	case "VALID", "ACTIVE":
		return 3
	case "EXPIRED", "INVALID", "INACTIVE", "DISABLED", "TERMINATED", "STOPPED":
		return -1
	}
	if b, ok := boolOf(field(m, "isActive", "active")); ok {
		if b {
			return 3
		}
		return -1
	}
	if t := timeOf(field(m, "endTime", "periodEndTime", "expireTime", "expirationTime")); t.After(time.Now()) {
		return 1
	}
	return 0
}
