package accounts

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
)

// The Token Plan's rolling windows (Alibaba Cloud's Personal/Solo plan and
// Qwen Cloud's Individual plan) come from one console API, called through
// the OneConsole gateway with the console's session and security token, as
// CodexBar reads them (Sources/CodexBarCore/Providers/Alibaba/
// AlibabaTokenPlanUsageFetcher.swift, Providers/QwenCloud/
// QwenCloudTokenPlanAPIClient.swift, Providers/Shared/AliyunOneConsole/
// OneConsoleTokenPlanSnapshot.swift).

const tokenPlanUsageAPI = "zeldaHttp.apikeyMgr./tokenplan/personal/api/v2/usage"

// tokenPlanConsole is one console the personal Token Plan is read from.
type tokenPlanConsole struct {
	// origin is the console the session belongs to; data is the gateway
	// host the API is called on.
	origin, data, dashboard, action, regionID string
}

// readTokenPlanWindows calls the usage API. The gateway sometimes answers
// a success without the windows; it is asked again, at most three times.
// errOtherRegion is an answer with no personal plan in it.
func readTokenPlanWindows(ctx context.Context, hc *http.Client, c tokenPlanConsole, cookie, secToken string) (usage.Reading, error) {
	cornerstone := map[string]any{"xsp_lang": "en-US"}
	if cna := cookieValue(cookie, "cna"); cna != "" {
		cornerstone["X-Anonymous-Id"] = cna
	}
	params := mustJSON(map[string]any{"Api": tokenPlanUsageAPI, "V": "1.0", "Data": map[string]any{"cornerstoneParam": cornerstone}})
	fields := [][2]string{{"product", "sfm_bailian"}, {"action", c.action}, {"region", c.regionID}, {"language", "en-US"}, {"params", params}}
	if secToken != "" {
		fields = append(fields, [2]string{"sec_token", secToken})
	}
	q := url.Values{"action": {c.action}, "product": {"sfm_bailian"}, "api": {tokenPlanUsageAPI}, "_v": {"undefined"}}
	var err error
	for range 3 {
		var body []byte
		body, err = doWeb(ctx, hc, http.MethodPost, c.data+"/data/api.json?"+q.Encode(), consoleHeader(cookie, c.origin, c.dashboard), formBody(fields))
		if err != nil {
			return usage.Reading{}, err
		}
		var r usage.Reading
		if r, err = parseTokenPlanWindows(body); !errors.Is(err, errNoWindowsYet) {
			return r, err
		}
	}
	return usage.Reading{}, fmt.Errorf("accounts: %s: %w", hostPath(c.data), errOtherRegion)
}

// errNoWindowsYet is a successful answer without the windows.
var errNoWindowsYet = errors.New("the answer carried no windows")

// parseTokenPlanWindows maps the 5-hour, weekly and monthly used ratios
// (0–1) and their resets.
func parseTokenPlanWindows(body []byte) (usage.Reading, error) {
	v, ok := decodeConsole(body)
	if !ok {
		if looksLikeSignIn(body) {
			return usage.Reading{}, fmt.Errorf("%w (the console asked to sign in)", usage.ErrAuth)
		}
		return usage.Reading{}, errors.New("accounts: token plan: the answer is not JSON")
	}
	if err := consoleError(v); err != nil {
		return usage.Reading{}, err
	}
	u := objectWith(v, "per5HourPercentage", "per1WeekPercentage", "per1MonthPercentage")
	r := usage.Reading{Scope: "account", Subscription: true}
	for _, w := range []struct {
		name, ratio, reset string
		d                  time.Duration
	}{
		{"5h", "per5HourPercentage", "per5HourResetTime", 5 * time.Hour},
		{"week", "per1WeekPercentage", "per1WeekResetTime", 7 * 24 * time.Hour},
		{"month", "per1MonthPercentage", "per1MonthResetTime", 30 * 24 * time.Hour},
	} {
		if u == nil {
			break
		}
		ratio, ok := numOf(u[w.ratio])
		if !ok {
			continue
		}
		r.Windows = append(r.Windows, usage.Window{Name: w.name, UsedPct: clampPct(ratio * 100), Duration: w.d, ResetsAt: timeOf(u[w.reset])})
	}
	if len(r.Windows) == 0 {
		return usage.Reading{}, errNoWindowsYet
	}
	return r, nil
}

// consoleError is the refusal or failure a OneConsole answer reports, nil
// for a success. A sign-in demand, an expired token and an authorisation
// refusal are usage.ErrAuth; a workspace the session may not use is not
// (the session is good, the account lacks the product).
func consoleError(v any) error {
	root, _ := v.(map[string]any)
	if b, ok := boolOf(root["successResponse"]); ok && !b {
		if code, ok := deepNumber(v, "statusCode", "status_code", "code"); ok && (code == 401 || code == 403) {
			return fmt.Errorf("%w (code %.0f)", usage.ErrAuth, code)
		}
		return classifyConsole(deepString(v, "errorCode", "code", "status", "statusCode"), deepString(v, "errorMsg", "message", "msg", "statusMessage"))
	}
	if frame := failingFrame(v); frame != nil {
		code := deepString(frame, "errorCode", "Code", "code")
		msg := deepString(frame, "errorMsg", "Message", "message", "msg")
		if msg == "" {
			msg = "request was not successful"
		}
		return classifyConsole(code, msg)
	}
	if code, ok := deepNumber(v, "statusCode", "status_code", "code"); ok && code != 0 && code != 200 {
		if code == 401 || code == 403 {
			return fmt.Errorf("%w (code %.0f)", usage.ErrAuth, code)
		}
		return fmt.Errorf("accounts: console code %.0f", code)
	}
	if err := classifyConsole(deepString(v, "errorCode", "code", "status", "statusCode"), deepString(v, "errorMsg", "message", "msg", "statusMessage")); errors.Is(err, usage.ErrAuth) {
		return err
	}
	return nil
}

// classifyConsole words a console failure: a refusal, or an error.
func classifyConsole(code, msg string) error {
	c := strings.ToLower(code + " " + msg)
	for _, s := range []string{"needlogin", "login", "postonlyortokenerror", "tokenerror", "request has expired", "refresh page", "请求已经过期"} {
		if strings.Contains(c, s) {
			return fmt.Errorf("%w (the console asked to sign in again)", usage.ErrAuth)
		}
	}
	if !strings.Contains(c, "workspace.notauthori") {
		for _, s := range []string{"notauthorised", "notauthorized", "not authorised", "not authorized", "unauthorised", "unauthorized", "access denied", "forbidden"} {
			if strings.Contains(c, s) {
				return fmt.Errorf("%w (the console refused the session)", usage.ErrAuth)
			}
		}
	}
	what := strings.TrimSpace(code + " " + msg)
	if what == "" {
		what = "request was not successful"
	}
	return fmt.Errorf("accounts: console: %s", what)
}

// failingFrame is the first object whose own success flag is false.
func failingFrame(v any) map[string]any {
	var found map[string]any
	walk(v, func(m map[string]any) bool {
		for _, k := range []string{"success", "Success"} {
			if b, ok := boolOf(m[k]); ok && !b {
				found = m
				return true
			}
		}
		return false
	})
	return found
}
