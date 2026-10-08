package accounts

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// readerStepFun registers the reader (readers_gen.go).
func readerStepFun() usage.Reader { return StepFun{} }

// StepFun reads the Step Plan's limits with an Oasis-Token, the session
// token StepFun's platform issues at sign-in, as CodexBar's StepFun
// provider does (Sources/CodexBarCore/Providers/StepFun/StepFunUsageFetcher.swift):
// a Coding Plan's rolling 5-hour and weekly windows, or a Token Plan's
// credit pool. `tokenops vendor-usage setup stepfun` signs in once with a
// username and password (Login) and stores only the token; the token is
// also taken pasted, from an Oasis-Token cookie header, or STEPFUN_TOKEN.
// An expired token is not renewed: setup is run again.
type StepFun struct {
	BaseURL string
	HTTP    *http.Client
}

func (StepFun) Endpoint() string               { return "stepfun" }
func (StepFun) Provider() eventschema.Provider { return "stepfun" }
func (StepFun) Source() string                 { return "stepfun-account" }

// stepfunDefaultWebID is the device ID StepFun's web client sends before
// it has a token to take one from.
const stepfunDefaultWebID = "c8a1002d2c457e758785a9979832217c7c0b884c"

func (s StepFun) root() string { return base(s.BaseURL, "https://platform.stepfun.com") }

// stepfunToken is the Oasis-Token in a pasted value: the token itself, or
// a Cookie header carrying it.
func stepfunToken(raw string) string {
	raw = strings.TrimSpace(raw)
	if v := cookieValue(raw, "Oasis-Token"); v != "" {
		return v
	}
	return raw
}

// stepfunWebID is the device_id claim of the token's refresh half (or its
// access half), which the Oasis-Webid header must match.
func stepfunWebID(token string) string {
	halves := strings.Split(token, "...")
	for i := len(halves) - 1; i >= 0; i-- {
		parts := strings.Split(halves[i], ".")
		if len(parts) < 2 {
			continue
		}
		payload, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(parts[1], "="))
		if err != nil {
			continue
		}
		var claims struct {
			DeviceID string `json:"device_id"`
		}
		if json.Unmarshal(payload, &claims) == nil && claims.DeviceID != "" {
			return claims.DeviceID
		}
	}
	return stepfunDefaultWebID
}

// send posts body to path with StepFun's web-client headers and cookie,
// and returns the status, the Set-Cookie headers and the body. The body
// may hold a password: it is never put in an error.
func (s StepFun) send(ctx context.Context, method, path, cookie, webID string, body []byte) (int, []string, []byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, s.root()+path, rd)
	if err != nil {
		return 0, nil, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Oasis-Appid", "10300")
	req.Header.Set("Oasis-Platform", "web")
	req.Header.Set("Oasis-Webid", webID)
	req.Header.Set("User-Agent", browserUA)
	if cookie != "" {
		req.Header.Set("Cookie", cookie)
	}
	resp, err := noRedirect(s.HTTP).Do(req)
	if err != nil {
		return 0, nil, nil, fmt.Errorf("accounts: %s %s: %w", method, hostPath(s.root()+path), err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return resp.StatusCode, resp.Header.Values("Set-Cookie"), data, nil
}

func (s StepFun) Read(ctx context.Context, key string) (usage.Reading, error) {
	token := stepfunToken(key)
	if token == "" {
		return usage.Reading{}, fmt.Errorf("%w (no Oasis-Token)", usage.ErrAuth)
	}
	webID := stepfunWebID(token)
	const path = "/api/step.openapi.devcenter.Dashboard/QueryStepPlanRateLimit"
	status, _, body, err := s.send(ctx, http.MethodPost, path, "Oasis-Token="+token+"; Oasis-Webid="+webID, webID, []byte("{}"))
	if err != nil {
		return usage.Reading{}, err
	}
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		return usage.Reading{}, fmt.Errorf("%w (%d on %s)", usage.ErrAuth, status, hostPath(s.root()+path))
	}
	if status != http.StatusOK {
		return usage.Reading{}, fmt.Errorf("accounts: POST %s: status %d", hostPath(s.root()+path), status)
	}
	return parseStepFun(body)
}

func parseStepFun(body []byte) (usage.Reading, error) {
	var resp struct {
		Status         number `json:"status"`
		Message        string `json:"message"`
		Desc           string `json:"desc"`
		FiveHourLeft   number `json:"five_hour_usage_left_rate"`
		WeeklyLeft     number `json:"weekly_usage_left_rate"`
		FiveHourReset  number `json:"five_hour_usage_reset_time"`
		WeeklyReset    number `json:"weekly_usage_reset_time"`
		PlanFamily     number `json:"plan_family"`
		PlanCreditRate *struct {
			SubscriptionLeft  number `json:"subscription_credit_left_rate"`
			SubscriptionReset number `json:"subscription_credit_reset_time"`
			TopupLeft         number `json:"topup_credit_left_rate"`
			Buckets           []struct {
				Total    number `json:"credit_total"`
				Residual number `json:"credit_residual"`
			} `json:"credit_buckets"`
		} `json:"plan_credit_rate_limit"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return usage.Reading{}, fmt.Errorf("accounts: stepfun: %w", err)
	}
	if !resp.Status.ok || resp.Status.v != 1 {
		msg := strings.ToLower(resp.Message + " " + resp.Desc)
		for _, w := range []string{"401", "403", "unauthorized", "unauthenticated", "auth failed", "invalid token", "token expired", "expired token", "login"} {
			if strings.Contains(msg, w) {
				return usage.Reading{}, fmt.Errorf("%w (StepFun refused the token)", usage.ErrAuth)
			}
		}
		return usage.Reading{}, errors.New("accounts: stepfun: the rate-limit query was not successful")
	}
	r := usage.Reading{Scope: "account", Subscription: true}
	liveWindow := resp.FiveHourReset.v > 0 || resp.WeeklyReset.v > 0
	cr := resp.PlanCreditRate
	creditPool := cr != nil && (cr.SubscriptionLeft.ok || cr.TopupLeft.ok || len(cr.Buckets) > 0)
	if !liveWindow && (creditPool || resp.PlanFamily.ok && resp.PlanFamily.v == 2) {
		// A Token Plan: one credit pool, its rate windows unset.
		if cr == nil {
			return r, nil
		}
		left, ok := stepfunCreditLeft(cr.Buckets, cr.SubscriptionLeft, cr.TopupLeft)
		if !ok {
			return r, nil
		}
		w := usage.Window{Name: "credits", UsedPct: clampPct((1 - left) * 100)}
		if cr.SubscriptionReset.v > 0 {
			w.ResetsAt = time.Unix(int64(cr.SubscriptionReset.v), 0).UTC()
		}
		r.Windows = []usage.Window{w}
		return r, nil
	}
	if !resp.FiveHourLeft.ok || !resp.WeeklyLeft.ok || !resp.FiveHourReset.ok || !resp.WeeklyReset.ok {
		return usage.Reading{}, errors.New("accounts: stepfun: no rate-limit windows in the answer")
	}
	r.Windows = []usage.Window{
		{Name: "5h", UsedPct: clampPct((1 - resp.FiveHourLeft.v) * 100), Duration: 5 * time.Hour, ResetsAt: time.Unix(int64(resp.FiveHourReset.v), 0).UTC()},
		{Name: "week", UsedPct: clampPct((1 - resp.WeeklyLeft.v) * 100), Duration: 7 * 24 * time.Hour, ResetsAt: time.Unix(int64(resp.WeeklyReset.v), 0).UTC()},
	}
	return r, nil
}

// stepfunCreditLeft is the share of credit left: the buckets' residual over
// their total when every bucket reports both, else the subscription rate,
// else the top-up rate (independent fractions cannot be added).
func stepfunCreditLeft(buckets []struct {
	Total    number `json:"credit_total"`
	Residual number `json:"credit_residual"`
}, sub, topup number) (float64, bool) {
	var total, residual float64
	valid := len(buckets) > 0
	for _, b := range buckets {
		if !b.Total.ok || !b.Residual.ok || b.Total.v <= 0 || b.Residual.v < 0 || b.Residual.v > b.Total.v {
			valid = false
			break
		}
		total, residual = total+b.Total.v, residual+b.Residual.v
	}
	switch {
	case valid:
		return residual / total, true
	case sub.ok:
		return sub.v, true
	case topup.ok:
		return topup.v, true
	}
	return 0, false
}

// Login signs in with a username and password the way StepFun's web client
// does (an ingress cookie, a registered device, then the password) and
// returns the Oasis-Token pair. The password goes only in the sign-in
// request's body and is never logged, stored or put in an error.
func (s StepFun) Login(ctx context.Context, username, password string) (string, error) {
	status, cookies, _, err := s.send(ctx, http.MethodGet, "/", "", stepfunDefaultWebID, nil)
	if err != nil {
		return "", err
	}
	ingress := ""
	for _, c := range cookies {
		if v, ok := strings.CutPrefix(c, "INGRESSCOOKIE="); ok {
			ingress, _, _ = strings.Cut(v, ";")
		}
	}
	if ingress == "" {
		return "", fmt.Errorf("accounts: stepfun: the platform set no ingress cookie (status %d)", status)
	}
	const passport = "/passport/proto.api.passport.v1.PassportService/"
	anon, err := s.tokenFrom(ctx, passport+"RegisterDevice", "INGRESSCOOKIE="+ingress, stepfunDefaultWebID, []byte("{}"))
	if err != nil {
		return "", err
	}
	webID := stepfunWebID(anon)
	body, err := json.Marshal(map[string]string{"username": username, "password": password})
	if err != nil {
		return "", err
	}
	return s.tokenFrom(ctx, passport+"SignInByPassword", "Oasis-Token="+anon+"; Oasis-Webid="+webID+"; INGRESSCOOKIE="+ingress, webID, body)
}

// tokenFrom posts to a passport call and returns the token pair it issues.
func (s StepFun) tokenFrom(ctx context.Context, path, cookie, webID string, body []byte) (string, error) {
	status, _, data, err := s.send(ctx, http.MethodPost, path, cookie, webID, body)
	if err != nil {
		return "", err
	}
	switch {
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return "", fmt.Errorf("%w (%d on %s)", usage.ErrAuth, status, hostPath(s.root()+path))
	case status != http.StatusOK:
		return "", fmt.Errorf("accounts: POST %s: status %d", hostPath(s.root()+path), status)
	}
	var resp struct {
		AccessToken  *struct{ Raw string } `json:"accessToken"`
		RefreshToken *struct{ Raw string } `json:"refreshToken"`
	}
	if err := json.Unmarshal(data, &resp); err != nil || resp.AccessToken == nil || resp.AccessToken.Raw == "" {
		return "", fmt.Errorf("%w (no token from %s: the username or password was not accepted)", usage.ErrAuth, hostPath(s.root()+path))
	}
	if resp.RefreshToken != nil && resp.RefreshToken.Raw != "" {
		return resp.AccessToken.Raw + "..." + resp.RefreshToken.Raw, nil
	}
	return resp.AccessToken.Raw, nil
}
