package accounts

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// readerIBMBob registers the reader (readers_gen.go).
func readerIBMBob() usage.Reader { return IBMBob{} }

// IBMBob reads the month's Bobcoins as CodexBar's IBM Bob provider does:
// GET /admin/v1/profile lists the key's subscription instances and teams,
// then GET /admin/v1/teams/{team}/users/{user} on each instance's regional
// host gives the user's usage against the team budget. Bobcoins are IBM
// Bob's own unit, not dollars, so the reading is a window, not spend. The
// key goes only to api.us-east.bob.ibm.com and regional hosts under
// bob.ibm.com.
type IBMBob struct {
	// BaseURL replaces both the profile host and every regional host.
	BaseURL string
	HTTP    *http.Client
}

func (IBMBob) Endpoint() string               { return "ibmbob" }
func (IBMBob) Provider() eventschema.Provider { return "ibmbob" }
func (IBMBob) Source() string                 { return "ibmbob-account" }

type ibmBobProfile struct {
	Instances []struct {
		InstanceID   string `json:"instance_id"`
		UserID       string `json:"user_id"`
		RefreshAt    stamp  `json:"refresh_at"`
		RegionDomain string `json:"region_domain"`
		Teams        []struct {
			ID          string `json:"id"`
			BudgetLimit number `json:"budget_limit"`
		} `json:"teams"`
	} `json:"instances"`
}

func (b IBMBob) Read(ctx context.Context, key string) (usage.Reading, error) {
	auth := ibmBobAuthorization(key)
	var profile ibmBobProfile
	if err := doJSON(ctx, b.HTTP, http.MethodGet, base(b.BaseURL, "https://api.us-east.bob.ibm.com")+"/admin/v1/profile",
		http.Header{"Authorization": {auth}}, nil, &profile); err != nil {
		return usage.Reading{}, err
	}
	var used, limit float64
	teams, limited := 0, 0
	var reset time.Time
	for _, in := range profile.Instances {
		if in.UserID == "" {
			continue
		}
		regional, err := b.regionalBase(in.RegionDomain)
		if err != nil {
			return usage.Reading{}, err
		}
		for _, t := range in.Teams {
			if t.ID == "" {
				continue
			}
			var budget struct {
				Usage       number `json:"usage"`
				BudgetLimit number `json:"budget_limit"`
			}
			endpoint := regional + "/admin/v1/teams/" + url.PathEscape(t.ID) + "/users/" + url.PathEscape(in.UserID)
			header := http.Header{"Authorization": {auth}, "X-Instance-Id": {in.InstanceID}, "X-Team-Id": {t.ID}}
			if err := doJSON(ctx, b.HTTP, http.MethodGet, endpoint, header, nil, &budget); err != nil {
				return usage.Reading{}, err
			}
			teams++
			used += max(0, budget.Usage.v)
			l := budget.BudgetLimit
			if !l.ok {
				l = t.BudgetLimit
			}
			if l.ok && l.v >= 0 {
				limit += l.v
				limited++
			}
			if r := in.RefreshAt.t; !r.IsZero() && (reset.IsZero() || r.Before(reset)) {
				reset = r
			}
		}
	}
	r := usage.Reading{Scope: "account"}
	// Without a budget for every team there is no share to report.
	if teams == 0 || limited != teams || limit <= 0 {
		return r, nil
	}
	r.Subscription = true
	r.Windows = []usage.Window{{Name: "month", UsedPct: clampPct(pct(used, limit)), ResetsAt: reset}}
	return r, nil
}

// regionalBase is an instance's API host: api.<region_domain>, only under
// bob.ibm.com and only over HTTPS; the default host when none is given.
func (b IBMBob) regionalBase(domain string) (string, error) {
	if b.BaseURL != "" {
		return base(b.BaseURL, ""), nil
	}
	domain = strings.ToLower(strings.TrimSpace(domain))
	if domain == "" {
		return "https://api.us-east.bob.ibm.com", nil
	}
	host := domain
	if !strings.HasPrefix(host, "api.") {
		host = "api." + host
	}
	u, err := url.Parse("https://" + host)
	if err != nil || u.Host != host || u.Port() != "" || u.User != nil || (u.Hostname() != "bob.ibm.com" && !strings.HasSuffix(u.Hostname(), ".bob.ibm.com")) {
		return "", fmt.Errorf("accounts: IBM Bob named an untrusted regional host %q", host)
	}
	return "https://" + host, nil
}

// ibmBobAuthorization is "Bearer <jwt>" for an IAM token and "Apikey <key>"
// for an API key, as IBM Bob's API takes them.
func ibmBobAuthorization(key string) string {
	parts := strings.Split(key, ".")
	if len(parts) == 3 {
		if payload, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(parts[1], "=")); err == nil {
			var claims map[string]any
			if json.Unmarshal(payload, &claims) == nil {
				return "Bearer " + key
			}
		}
	}
	return "Apikey " + key
}
