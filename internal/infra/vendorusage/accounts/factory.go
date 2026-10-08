package accounts

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// readerFactory and readerFactoryWeb register the readers (readers_gen.go).
func readerFactory() usage.Reader    { return Factory{} }
func readerFactoryWeb() usage.Reader { return FactoryWeb{} }

// Factory reads a Factory (Droid) plan's usage with a Factory API key
// (fk-…, FACTORY_API_KEY), as CodexBar's Factory provider does
// (Sources/CodexBarCore/Providers/Factory/FactoryStatusProbe.swift,
// FactoryStatusProbe+APIKey.swift, docs/factory.md): a plan on token rate
// limits has 5-hour, weekly and monthly windows (and the same for its
// Core fallback models), read from api.factory.ai/api/billing/limits;
// an older plan has its Standard and Premium token allowances for the
// billing period, from the subscription usage. The extra-usage balance is
// prepaid credit beyond the plan.
type Factory struct {
	// API and App replace api.factory.ai and app.factory.ai in tests.
	API, App string
	HTTP     *http.Client
	Now      func() time.Time
}

func (Factory) Endpoint() string               { return "factory" }
func (Factory) Provider() eventschema.Provider { return "factory" }
func (Factory) Source() string                 { return "factory-account" }
func (Factory) KeyOnly() bool                  { return true }

func (f Factory) Read(ctx context.Context, key string) (usage.Reading, error) {
	if isSession(key) {
		return usage.Reading{}, usage.ErrSkip
	}
	c := factoryClient{api: base(f.API, "https://api.factory.ai"), app: base(f.App, "https://app.factory.ai"), hc: f.HTTP, now: f.Now}
	return c.read(ctx, strings.TrimSpace(key), "")
}

// FactoryWeb reads the same figures with app.factory.ai's browser
// session: its cookies, with the access token they carry as the bearer.
type FactoryWeb struct {
	API, App string
	HTTP     *http.Client
	Now      func() time.Time
}

func (FactoryWeb) Endpoint() string               { return "factory" }
func (FactoryWeb) Provider() eventschema.Provider { return "factory" }
func (FactoryWeb) Source() string                 { return "factory-web" }

func (f FactoryWeb) Read(ctx context.Context, cookie string) (usage.Reading, error) {
	cookie = cookieHeader(cookie)
	if !isSession(cookie) {
		return usage.Reading{}, usage.ErrSkip
	}
	c := factoryClient{api: base(f.API, "https://api.factory.ai"), app: base(f.App, "https://app.factory.ai"), hc: f.HTTP, now: f.Now}
	r, err := c.read(ctx, factoryBearer(cookie), cookie)
	if errors.Is(err, usage.ErrAuth) && factoryBearer(cookie) != "" {
		// A stale access token beside a live session cookie: the cookie
		// alone may still be accepted.
		return c.read(ctx, "", cookie)
	}
	return r, err
}

// factorySessionCookies are the cookies that carry a Factory session, in
// the order CodexBar takes a bearer from them.
var factorySessionCookies = []string{"__Secure-next-auth.session-token", "next-auth.session-token",
	"__Secure-authjs.session-token", "authjs.session-token"}

// factoryBearer is the access token in the session cookies: access-token
// when it is a JWT, else a session token that is one.
func factoryBearer(cookie string) string {
	candidates := append([]string{"access-token"}, factorySessionCookies...)
	candidates = append(candidates, "session")
	for _, name := range candidates {
		if v := cookieValue(cookie, name); strings.Contains(v, ".") {
			return v
		}
	}
	return cookieValue(cookie, "access-token")
}

type factoryClient struct {
	api, app string
	hc       *http.Client
	now      func() time.Time
}

func (c factoryClient) header(bearer, cookie string) http.Header {
	h := http.Header{"Origin": {"https://app.factory.ai"}, "Referer": {"https://app.factory.ai/"}, "X-Factory-Client": {"web-app"}}
	if bearer != "" {
		h.Set("Authorization", "Bearer "+bearer)
	}
	if cookie != "" {
		h.Set("Cookie", cookie)
		h.Set("User-Agent", browserUA)
	}
	return h
}

func (c factoryClient) get(ctx context.Context, u string, h http.Header, out any) error {
	if h.Get("Cookie") != "" {
		return webJSON(ctx, c.hc, u, h, out)
	}
	return doJSON(ctx, c.hc, http.MethodGet, u, h, nil, out)
}

func (c factoryClient) read(ctx context.Context, bearer, cookie string) (usage.Reading, error) {
	h := c.header(bearer, cookie)
	var me struct {
		User *struct {
			ID string `json:"id"`
		} `json:"userProfile"`
	}
	var err error
	host := ""
	for _, root := range []string{c.api, c.app} {
		if err = c.get(ctx, root+"/api/app/auth/me", h, &me); err == nil {
			host = root
			break
		}
		if !errors.Is(err, usage.ErrAuth) {
			continue
		}
		return usage.Reading{}, err
	}
	if host == "" {
		return usage.Reading{}, err
	}
	var limits factoryLimits
	if c.get(ctx, c.api+"/api/billing/limits", h, &limits) == nil && limits.TokenRateLimits && limits.Limits != nil && limits.Limits.Standard != nil {
		return c.rateLimited(limits), nil
	}
	userID := ""
	if me.User != nil {
		userID = me.User.ID
	}
	if userID == "" {
		userID = jwtSubject(bearer)
	}
	q := url.Values{"useCache": {"true"}}
	if userID != "" {
		q.Set("userId", userID)
	}
	var sub struct {
		Usage *struct {
			End      stamp        `json:"endDate"`
			Standard *factoryPool `json:"standard"`
			Premium  *factoryPool `json:"premium"`
		} `json:"usage"`
	}
	if err := c.get(ctx, host+"/api/organization/subscription/usage?"+q.Encode(), h, &sub); err != nil {
		return usage.Reading{}, err
	}
	if sub.Usage == nil {
		return usage.Reading{}, errors.New("accounts: GET factory.ai subscription usage: no usage")
	}
	r := usage.Reading{Scope: "account", Subscription: true}
	for _, p := range []struct {
		name string
		pool *factoryPool
	}{{"standard", sub.Usage.Standard}, {"premium", sub.Usage.Premium}} {
		if p.pool != nil {
			r.Windows = append(r.Windows, usage.Window{Name: p.name, UsedPct: p.pool.percent(), ResetsAt: sub.Usage.End.t})
		}
	}
	if len(r.Windows) == 0 {
		return usage.Reading{}, errors.New("accounts: GET factory.ai subscription usage: no token pools")
	}
	return r, nil
}

type factoryLimits struct {
	TokenRateLimits bool    `json:"usesTokenRateLimitsBilling"`
	ExtraCents      *number `json:"extraUsageBalanceCents"`
	Limits          *struct {
		Standard *factoryTier `json:"standard"`
		Core     *factoryTier `json:"core"`
	} `json:"limits"`
}

type factoryTier struct {
	FiveHour *factoryWindow `json:"fiveHour"`
	Weekly   *factoryWindow `json:"weekly"`
	Monthly  *factoryWindow `json:"monthly"`
}

type factoryWindow struct {
	UsedPct   *float64 `json:"usedPercent"`
	End       stamp    `json:"windowEnd"`
	SecondsTo *float64 `json:"secondsRemaining"`
}

// hasData is a window Factory filled in: some use, or a known end.
func (w *factoryWindow) hasData() bool {
	return w != nil && w.UsedPct != nil && (*w.UsedPct > 0 || !w.End.t.IsZero() || w.SecondsTo != nil)
}

func (c factoryClient) rateLimited(l factoryLimits) usage.Reading {
	now := time.Now
	if c.now != nil {
		now = c.now
	}
	r := usage.Reading{Scope: "account", Subscription: true}
	add := func(prefix string, t *factoryTier, onlyWithData bool) {
		if t == nil {
			return
		}
		for _, w := range []struct {
			name string
			d    time.Duration
			win  *factoryWindow
		}{{"5h", 5 * time.Hour, t.FiveHour}, {"week", 7 * 24 * time.Hour, t.Weekly}, {"month", 0, t.Monthly}} {
			if w.win == nil || w.win.UsedPct == nil || (onlyWithData && !w.win.hasData()) {
				continue
			}
			out := usage.Window{Name: prefix + w.name, UsedPct: clampPct(*w.win.UsedPct), Duration: w.d}
			switch {
			case w.win.SecondsTo != nil && *w.win.SecondsTo > 0:
				out.ResetsAt = now().Add(time.Duration(*w.win.SecondsTo * float64(time.Second))).UTC().Truncate(time.Second)
			case w.win.End.t.After(now()):
				out.ResetsAt = w.win.End.t
			case !w.win.End.t.IsZero() && w.win.SecondsTo == nil:
				out.UsedPct = 0 // the window ended and has not restarted
			}
			r.Windows = append(r.Windows, out)
		}
	}
	add("", l.Limits.Standard, false)
	add("core ", l.Limits.Core, true)
	if l.ExtraCents != nil && l.ExtraCents.ok && l.ExtraCents.v >= 0 {
		r.BalanceUSD, r.HasBalance = l.ExtraCents.v/100, true
	}
	return r
}

type factoryPool struct {
	UserTokens number `json:"userTokens"`
	Allowance  number `json:"totalAllowance"`
	Ratio      number `json:"usedRatio"`
}

// percent is the share of the pool used: Factory's ratio when it is one,
// else the tokens against the allowance (an allowance above 1e12 is
// unlimited, shown against 100M tokens as CodexBar does).
func (p *factoryPool) percent() float64 {
	used, allowance := max(0, p.UserTokens.v), p.Allowance.v
	reliable := allowance > 0 && allowance <= 1e12
	if p.Ratio.ok && !(p.Ratio.v == 0 && used > 0 && reliable) {
		switch {
		case p.Ratio.v >= -0.001 && p.Ratio.v <= 1.001:
			return clampPct(p.Ratio.v * 100)
		case !reliable && p.Ratio.v >= -0.1 && p.Ratio.v <= 100.1:
			return clampPct(p.Ratio.v)
		}
	}
	switch {
	case allowance > 1e12:
		return clampPct(used / 1e8 * 100)
	case allowance <= 0:
		return 0
	}
	return clampPct(pct(used, allowance))
}

// jwtSubject is a JWT's sub claim, unverified: it only names the user in
// a query the token itself authorises.
func jwtSubject(token string) string {
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return ""
	}
	payload, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(parts[1], "="))
	if err != nil {
		return ""
	}
	var claims struct {
		Sub string `json:"sub"`
	}
	if json.Unmarshal(payload, &claims) != nil {
		return ""
	}
	return claims.Sub
}
