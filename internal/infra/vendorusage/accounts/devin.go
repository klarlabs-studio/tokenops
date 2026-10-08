package accounts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
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

// Devin reads the organisation's daily and weekly quota from the Devin
// web app's own call, GET app.devin.ai/api/<org>/billing/quota/usage, as
// CodexBar's Devin provider does
// (Sources/CodexBarCore/Providers/Devin/DevinUsageFetcher.swift,
// DevinUsageSnapshot.swift). The credential is "ORG:TOKEN": the
// organisation (its slug, its internal "org_…" ID, or its app.devin.ai
// URL) and the bearer token the web app sends. The extra-usage balance is
// prepaid credit beyond the quota.
type Devin struct {
	BaseURL string
	HTTP    *http.Client
}

func (Devin) Endpoint() string               { return "devin" }
func (Devin) Provider() eventschema.Provider { return "devin" }
func (Devin) Source() string                 { return "devin-web" }

func (d Devin) Read(ctx context.Context, credential string) (usage.Reading, error) {
	org, token, ok := devinCredential(credential)
	if !ok {
		return usage.Reading{}, fmt.Errorf("%w (Devin needs ORG:TOKEN, the organisation and the web app's bearer token)", usage.ErrAuth)
	}
	header := http.Header{
		"Authorization":   {"Bearer " + token},
		"Accept-Language": {"en-US,en;q=0.9"},
		"User-Agent":      {browserUA},
	}
	if org.internal != "" {
		header.Set("x-cog-org-id", org.internal)
	}
	root := base(d.BaseURL, "https://app.devin.ai") + "/api/"
	var lastErr error
	for _, path := range org.paths() {
		body, err := devinGet(ctx, d.HTTP, root+path+"/billing/quota/usage", header)
		if err != nil {
			if errors.Is(err, usage.ErrAuth) {
				return usage.Reading{}, err
			}
			lastErr = err
			continue
		}
		return devinReading(body)
	}
	return usage.Reading{}, lastErr
}

// devinGet sends one request; a 401 or 403 is a refusal (an expired
// token, or one with no organisation, as Devin says in the body).
func devinGet(ctx context.Context, hc *http.Client, u string, header http.Header) (json.RawMessage, error) {
	var raw json.RawMessage
	err := doJSON(ctx, hc, http.MethodGet, u, header, nil, &raw)
	return raw, err
}

type devinOrg struct{ slug, internal string }

// paths are the organisation paths Devin's app is known to answer, in
// CodexBar's order.
func (o devinOrg) paths() []string {
	if o.internal != "" {
		id := url.PathEscape(o.internal)
		return []string{id, "organizations/" + id}
	}
	slug := url.PathEscape(o.slug)
	return []string{"org/" + slug, slug}
}

// devinCredential splits "ORG:TOKEN" at the last colon (an organisation
// given as its URL has one of its own) and normalises both halves.
func devinCredential(credential string) (devinOrg, string, bool) {
	credential = strings.TrimSpace(credential)
	if strings.HasPrefix(credential, "{") {
		return devinLocalStorage(credential)
	}
	i := strings.LastIndex(credential, ":")
	if i <= 0 {
		return devinOrg{}, "", false
	}
	token := strings.TrimSpace(credential[i+1:])
	if lower := strings.ToLower(token); strings.HasPrefix(lower, "bearer ") {
		token = strings.TrimSpace(token[len("bearer "):])
	}
	org, ok := devinOrganization(credential[:i])
	if !ok || token == "" || strings.ContainsAny(token, " \r\n") {
		return devinOrg{}, "", false
	}
	return org, token, true
}

// devinOrgKey is app.devin.ai's localStorage key naming the internal ID of
// the organisation last used; the key ends in the organisation's slug.
const devinOrgKey = "last-internal-org-for-external-org-v1-"

// devinLocalStorage reads the session setup read from app.devin.ai's
// localStorage (ADR 0013 §8): the auth1 session's token ({"token":
// "auth1_…"} under a key ending in auth1_session) and the organisation
// last used, as CodexBar's DevinSessionImporter does.
func devinLocalStorage(bundle string) (devinOrg, string, bool) {
	var entries map[string]string
	if json.Unmarshal([]byte(bundle), &entries) != nil {
		return devinOrg{}, "", false
	}
	keys := make([]string, 0, len(entries))
	for k := range entries {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var (
		org   devinOrg
		token string
	)
	for _, k := range keys {
		v := entries[k]
		switch {
		case strings.HasSuffix(k, "auth1_session") && token == "":
			var s struct {
				Token string `json:"token"`
			}
			if json.Unmarshal([]byte(v), &s) == nil && strings.HasPrefix(s.Token, "auth1_") {
				token = s.Token
			}
		case strings.HasPrefix(k, devinOrgKey) && org.internal == "":
			slug := strings.TrimPrefix(k, devinOrgKey)
			if id := strings.Trim(strings.TrimSpace(v), `"`); id != "" && slug != "" && slug != "null" {
				org = devinOrg{slug: slug, internal: id}
			}
		}
	}
	return org, token, token != "" && org.internal != ""
}

// devinOrganization reads an organisation given as a slug, an internal
// ID ("org_…" or "org-…"), or an app.devin.ai URL (/org/<slug>/… or
// /organizations/<id>).
func devinOrganization(raw string) (devinOrg, bool) {
	raw = strings.TrimSpace(raw)
	if u, err := url.Parse(raw); err == nil && u.Host != "" {
		if u.Hostname() != "devin.ai" && !strings.HasSuffix(u.Hostname(), ".devin.ai") {
			return devinOrg{}, false
		}
		parts := strings.Split(strings.Trim(u.Path, "/"), "/")
		if len(parts) < 2 || (parts[0] != "org" && parts[0] != "organizations") {
			return devinOrg{}, false
		}
		raw = parts[1]
	}
	raw = strings.Trim(raw, "/")
	if strings.HasPrefix(raw, "org/") {
		raw = strings.TrimPrefix(raw, "org/")
	} else if strings.HasPrefix(raw, "organizations/") {
		raw = strings.TrimPrefix(raw, "organizations/")
	}
	if raw == "" || raw == "." || raw == ".." || strings.ContainsAny(raw, "/?#") {
		return devinOrg{}, false
	}
	if strings.HasPrefix(raw, "org_") || strings.HasPrefix(raw, "org-") {
		return devinOrg{internal: raw}, true
	}
	return devinOrg{slug: raw}, true
}

// devinReading maps the quota answer: the current shape's
// daily_percentage and weekly_percentage (a share under 1 is a fraction),
// else the first object under a "daily" or "weekly" key, as CodexBar's
// parser falls back to.
func devinReading(body json.RawMessage) (usage.Reading, error) {
	var root map[string]any
	if err := json.Unmarshal(body, &root); err != nil || root == nil {
		return usage.Reading{}, errors.New("accounts: GET app.devin.ai quota: unexpected shape")
	}
	r := usage.Reading{Scope: "account", Subscription: true}
	hideDaily, _ := root["hide_daily_quota"].(bool)
	if !hideDaily {
		if w, ok := devinWindow(root, "daily_percentage", "daily_reset_at", []string{"daily", "day"}); ok {
			w.Name, w.Duration = "day", 24*time.Hour
			r.Windows = append(r.Windows, w)
		}
	}
	if w, ok := devinWindow(root, "weekly_percentage", "weekly_reset_at", []string{"weekly", "week"}); ok {
		w.Name, w.Duration = "week", 7*24*time.Hour
		r.Windows = append(r.Windows, w)
	}
	if len(r.Windows) == 0 {
		return usage.Reading{}, errors.New("accounts: GET app.devin.ai quota: no quota windows")
	}
	if v, ok := devinNumber(root["overage_balance"]); ok && v >= 0 {
		r.BalanceUSD, r.HasBalance = v, true
	} else if v, ok := devinNumber(root["overage_balance_cents"]); ok && v >= 0 {
		r.BalanceUSD, r.HasBalance = v/100, true
	}
	return r, nil
}

func devinWindow(root map[string]any, pctKey, resetKey string, names []string) (usage.Window, bool) {
	if v, ok := devinNumber(root[pctKey]); ok {
		if v < 1 {
			v *= 100
		}
		return usage.Window{UsedPct: clampPct(v), ResetsAt: devinTime(root[resetKey])}, true
	}
	obj := devinFind(root, names)
	if obj == nil {
		return usage.Window{}, false
	}
	return devinObjectWindow(obj)
}

// devinFind is the first object, depth first in key order, under a key
// whose name contains one of names (and not "hide").
func devinFind(node map[string]any, names []string) map[string]any {
	keys := make([]string, 0, len(node))
	for k := range node {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		obj, ok := node[k].(map[string]any)
		if !ok {
			continue
		}
		lower := strings.ToLower(k)
		if !strings.Contains(lower, "hide") {
			for _, n := range names {
				if strings.Contains(lower, n) {
					return obj
				}
			}
		}
		if found := devinFind(obj, names); found != nil {
			return found
		}
	}
	return nil
}

func devinObjectWindow(obj map[string]any) (usage.Window, bool) {
	w := usage.Window{}
	for k, v := range obj {
		if strings.Contains(strings.ToLower(k), "reset") {
			if t := devinTime(v); !t.IsZero() {
				w.ResetsAt = t
			}
		}
	}
	scale := func(v float64) float64 {
		if v <= 1 {
			return v * 100
		}
		return v
	}
	if v, ok := devinFirst(obj, "used_percent", "usedPercent", "usage_percent", "usagePercent", "percent_used", "percentUsed", "percent"); ok {
		w.UsedPct = clampPct(scale(v))
		return w, true
	}
	if v, ok := devinFirst(obj, "remaining_percent", "remainingPercent", "percent_remaining", "percentRemaining"); ok {
		w.UsedPct = clampPct(100 - scale(v))
		return w, true
	}
	limit, hasLimit := devinFirst(obj, "limit", "quota", "total", "max", "available")
	if !hasLimit || limit <= 0 {
		return usage.Window{}, false
	}
	if used, ok := devinFirst(obj, "used", "usage", "used_count", "usedCount", "consumed"); ok {
		w.UsedPct = clampPct(pct(used, limit))
		return w, true
	}
	if left, ok := devinFirst(obj, "remaining", "left"); ok {
		w.UsedPct = clampPct(pct(limit-left, limit))
		return w, true
	}
	return usage.Window{}, false
}

func devinFirst(obj map[string]any, keys ...string) (float64, bool) {
	for _, k := range keys {
		if v, ok := devinNumber(obj[k]); ok {
			return v, true
		}
	}
	return 0, false
}

// devinNumber reads a JSON number or numeric string; a boolean is not one.
func devinNumber(v any) (float64, bool) {
	var n number
	switch x := v.(type) {
	case float64:
		n.v, n.ok = x, true
	case string:
		_ = n.UnmarshalJSON([]byte(x))
	}
	if !n.ok || math.IsNaN(n.v) || math.IsInf(n.v, 0) {
		return 0, false
	}
	return n.v, true
}

func devinTime(v any) time.Time {
	var s stamp
	switch x := v.(type) {
	case string:
		_ = s.UnmarshalJSON([]byte(x))
	case float64:
		b, _ := json.Marshal(x)
		_ = s.UnmarshalJSON(b)
	}
	return s.t
}
