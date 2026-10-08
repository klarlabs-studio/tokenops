package accounts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// readerDevin registers the reader (readers_gen.go).
func readerDevin() usage.Reader { return Devin{} }

// Devin reads Devin's daily and weekly quota from app.devin.ai's
// /api/<org>/billing/quota/usage, as CodexBar's Devin provider does, with
// the web session app.devin.ai keeps in localStorage: its auth1 token and
// the organisation it last used. Setup reads them from the browser
// (never the daemon), or the operator pastes AUTH1_TOKEN:ORGANIZATION.
type Devin struct {
	BaseURL string
	HTTP    *http.Client
}

func (Devin) Endpoint() string               { return "devin" }
func (Devin) Provider() eventschema.Provider { return "devin" }
func (Devin) Source() string                 { return "devin-web" }

// devinOrgKey is the localStorage key naming the internal ID of the
// organisation last used for an external one: the key ends in its slug.
const devinOrgKey = "last-internal-org-for-external-org-v1-"

// devinChrome is the browser the session belongs to; the API serves the
// web app.
const devinChrome = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/143.0.0.0 Safari/537.36"

// devinSession is a session's token and organisation.
type devinSession struct {
	token, slug, orgID string
}

// parseDevinSession reads the credential: the localStorage bundle setup
// read ({"<...>auth1_session": "{\"token\":...}", "last-internal-org-...-<slug>": "<id>"}),
// or a pasted AUTH1_TOKEN:ORGANIZATION, the organisation an internal ID
// (org-...) or a slug.
func parseDevinSession(credential string) (devinSession, bool) {
	credential = strings.TrimSpace(credential)
	if !strings.HasPrefix(credential, "{") {
		i := strings.LastIndex(credential, ":")
		if i <= 0 || i == len(credential)-1 {
			return devinSession{}, false
		}
		token, org := strings.TrimSpace(credential[:i]), strings.TrimSpace(credential[i+1:])
		token = strings.TrimPrefix(token, "Bearer ")
		s := devinSession{token: token}
		if strings.HasPrefix(org, "org-") || strings.HasPrefix(org, "org_") {
			s.orgID = org
		} else {
			s.slug = strings.TrimPrefix(org, "org/")
		}
		return s, token != ""
	}
	var bundle map[string]string
	if json.Unmarshal([]byte(credential), &bundle) != nil {
		return devinSession{}, false
	}
	names := make([]string, 0, len(bundle))
	for k := range bundle {
		names = append(names, k)
	}
	sort.Strings(names)
	var s devinSession
	for _, k := range names {
		v := bundle[k]
		switch {
		case strings.HasSuffix(k, "auth1_session") && s.token == "":
			var sess struct {
				Token string `json:"token"`
			}
			if json.Unmarshal([]byte(v), &sess) == nil && strings.HasPrefix(sess.Token, "auth1_") {
				s.token = sess.Token
			}
		case strings.HasPrefix(k, devinOrgKey) && s.orgID == "":
			slug := strings.TrimPrefix(k, devinOrgKey)
			if id := strings.Trim(strings.TrimSpace(v), `"`); id != "" && slug != "null" {
				s.orgID, s.slug = id, slug
			}
		}
	}
	return s, s.token != ""
}

// paths are the quota routes CodexBar tries, by internal ID, then slug.
func (s devinSession) paths() []string {
	var out []string
	if s.orgID != "" {
		out = append(out, url.PathEscape(s.orgID)+"/billing/quota/usage")
	}
	if s.slug != "" {
		out = append(out, "org/"+url.PathEscape(s.slug)+"/billing/quota/usage")
	}
	if s.orgID != "" {
		out = append(out, "organizations/"+url.PathEscape(s.orgID)+"/billing/quota/usage")
	}
	return out
}

func (d Devin) Read(ctx context.Context, credential string) (usage.Reading, error) {
	s, ok := parseDevinSession(credential)
	if !ok {
		return usage.Reading{}, fmt.Errorf("%w (not a Devin session)", usage.ErrAuth)
	}
	paths := s.paths()
	if len(paths) == 0 {
		return usage.Reading{}, errors.New("accounts: the Devin session names no organisation; paste AUTH1_TOKEN:ORGANIZATION")
	}
	header := http.Header{
		"Authorization":   {"Bearer " + s.token},
		"Accept-Language": {"en-US,en;q=0.9"},
		"User-Agent":      {devinChrome},
	}
	if s.orgID != "" {
		header.Set("x-cog-org-id", s.orgID)
	}
	var lastErr error
	for _, p := range paths {
		var resp map[string]json.RawMessage
		err := doJSON(ctx, d.HTTP, http.MethodGet, base(d.BaseURL, "https://app.devin.ai")+"/api/"+p, header, nil, &resp)
		if errors.Is(err, usage.ErrAuth) {
			return usage.Reading{}, err
		}
		if err != nil {
			lastErr = err
			continue
		}
		return parseDevinQuota(resp)
	}
	return usage.Reading{}, lastErr
}

// parseDevinQuota maps the quota answer: daily and weekly percentages
// (a value up to 1 is a fraction), their resets, and the overage balance.
func parseDevinQuota(resp map[string]json.RawMessage) (usage.Reading, error) {
	var (
		daily, weekly           number
		dailyReset, weeklyReset stamp
		hideDaily               bool
		overage, overageCents   number
	)
	get := func(k string, v any) { _ = json.Unmarshal(resp[k], v) }
	get("daily_percentage", &daily)
	get("weekly_percentage", &weekly)
	get("daily_reset_at", &dailyReset)
	get("weekly_reset_at", &weeklyReset)
	get("hide_daily_quota", &hideDaily)
	get("overage_balance", &overage)
	get("overage_balance_cents", &overageCents)
	pctOf := func(n number) float64 {
		if n.v <= 1 {
			return n.v * 100
		}
		return n.v
	}
	r := usage.Reading{Scope: "account"}
	if daily.ok && !hideDaily {
		r.Windows = append(r.Windows, usage.Window{Name: "day", UsedPct: clampPct(pctOf(daily)), Duration: 24 * time.Hour, ResetsAt: dailyReset.t})
	}
	if weekly.ok {
		r.Windows = append(r.Windows, usage.Window{Name: "week", UsedPct: clampPct(pctOf(weekly)), Duration: 7 * 24 * time.Hour, ResetsAt: weeklyReset.t})
	}
	switch {
	case overage.ok && overage.v >= 0:
		r.BalanceUSD, r.HasBalance = overage.v, true
	case overageCents.ok && overageCents.v >= 0:
		r.BalanceUSD, r.HasBalance = overageCents.v/100, true
	}
	if len(r.Windows) == 0 && !r.HasBalance {
		return usage.Reading{}, errors.New("accounts: GET app.devin.ai/api/.../billing/quota/usage: unexpected answer")
	}
	r.Subscription = len(r.Windows) > 0
	return r, nil
}
