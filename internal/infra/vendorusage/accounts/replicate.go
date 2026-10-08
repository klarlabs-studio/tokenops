package accounts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// readerReplicate registers the reader (readers_gen.go).
func readerReplicate() usage.Reader { return Replicate{} }

// Replicate reads this month's spend and the prepaid credit Replicate's
// billing page shows, ported from CodexBar
// (Sources/CodexBarCore/Resources/Plugins/replicate.ts,
// Providers/Replicate, docs/replicate.md), with the replicate.com website
// session (sessionid; csrftoken is optional). Replicate's API token does
// not read billing and is never used here.
//
// The billing page names the account (a user or an organisation) in its
// React props; that account's current monthly-usage invoice is the spend,
// and its unused credit, when Replicate reports it, the balance. There is
// no spending limit to read.
type Replicate struct {
	BaseURL string
	HTTP    *http.Client
	// Now decides which invoice is current; nil is time.Now.
	Now func() time.Time
}

func (Replicate) Endpoint() string               { return "replicate" }
func (Replicate) Provider() eventschema.Provider { return "replicate" }
func (Replicate) Source() string                 { return "replicate-account" }

var (
	replicateProps     = regexp.MustCompile(`(?is)<script\b([^>]*)>(.*?)</script\s*>`)
	replicatePropsID   = regexp.MustCompile(`(?i)\bid\s*=\s*["']react-component-props`)
	replicatePropsType = regexp.MustCompile(`(?i)\btype\s*=\s*["']application/json["']`)
	replicateSignIn    = regexp.MustCompile(`(?i)<title>\s*Sign in\s*\|\s*Replicate\s*</title>`)
)

type replicateAccount struct {
	kind, username string
}

func (r Replicate) Read(ctx context.Context, key string) (usage.Reading, error) {
	session := sessionToken(key, "sessionid")
	if session == "" {
		return usage.Reading{}, fmt.Errorf("%w (no sessionid in the Replicate session)", usage.ErrAuth)
	}
	cookie := "sessionid=" + session
	if csrf := cookieValue(key, "csrftoken"); csrf != "" {
		cookie += "; csrftoken=" + csrf
	}
	site := base(r.BaseURL, "https://replicate.com")
	page, err := doWeb(ctx, r.HTTP, http.MethodGet, site+"/account/billing", http.Header{
		"Cookie": {cookie}, "Accept": {"text/html"}, "User-Agent": {browserUA},
	}, nil)
	if err != nil {
		return usage.Reading{}, err
	}
	acct, ok := replicateBillingAccount(page)
	if !ok {
		if replicateSignIn.Match(page) {
			return usage.Reading{}, fmt.Errorf("%w (Replicate answered with its sign-in page)", usage.ErrAuth)
		}
		return usage.Reading{}, errors.New("accounts: replicate billing page: no account in its props")
	}
	kind := "users"
	if acct.kind == "organization" {
		kind = "organizations"
	}
	api := site + "/api/" + kind + "/" + url.PathEscape(acct.username)
	header := http.Header{"Cookie": {cookie}, "User-Agent": {browserUA}}
	var invoices struct {
		Invoices *[]struct {
			Type   string  `json:"type"`
			Ended  *string `json:"ended_before"`
			Before string  `json:"total_cost_before_adjustments"`
		} `json:"invoices"`
	}
	if err := doJSON(ctx, r.HTTP, http.MethodGet, api+"/invoices", header, nil, &invoices); err != nil {
		return usage.Reading{}, err
	}
	if invoices.Invoices == nil {
		return usage.Reading{}, errors.New("accounts: replicate invoices: unrecognised answer")
	}
	now := time.Now()
	if r.Now != nil {
		now = r.Now()
	}
	spent, found := 0.0, false
	for _, inv := range *invoices.Invoices {
		if inv.Type != "monthly-usage" {
			continue
		}
		if inv.Ended != nil {
			end := parseTime(*inv.Ended)
			if end.IsZero() || !end.After(now) {
				continue
			}
		}
		v, ok := replicateMoney(inv.Before)
		if !ok {
			return usage.Reading{}, errors.New("accounts: replicate invoices: unreadable spend")
		}
		spent, found = v, true
		break
	}
	if !found {
		return usage.Reading{}, errors.New("accounts: replicate invoices: no current monthly-usage invoice")
	}
	out := usage.Reading{Scope: "account", UsedUSD: spent, HasUsed: true}
	var credit struct {
		Unused string `json:"unused_credit"`
	}
	if doJSON(ctx, r.HTTP, http.MethodGet, api+"/unused-credit", header, nil, &credit) == nil {
		if v, ok := replicateMoney(credit.Unused); ok {
			out.BalanceUSD, out.HasBalance = v, true
		}
	}
	return out, nil
}

// replicateBillingAccount finds the user or organisation the billing page
// is for, in its JSON React props.
func replicateBillingAccount(page []byte) (replicateAccount, bool) {
	for _, m := range replicateProps.FindAllSubmatch(page, 200) {
		if !replicatePropsID.Match(m[1]) || !replicatePropsType.Match(m[1]) {
			continue
		}
		var v any
		if json.Unmarshal(m[2], &v) != nil {
			continue
		}
		if a, ok := replicateFindAccount(v, 0); ok {
			return a, true
		}
	}
	return replicateAccount{}, false
}

func replicateFindAccount(v any, depth int) (replicateAccount, bool) {
	if depth > 24 {
		return replicateAccount{}, false
	}
	switch x := v.(type) {
	case map[string]any:
		if a, ok := x["account"].(map[string]any); ok {
			kind, _ := a["kind"].(string)
			name, _ := a["username"].(string)
			if (kind == "user" || kind == "organization") && strings.TrimSpace(name) != "" {
				return replicateAccount{kind: kind, username: strings.TrimSpace(name)}, true
			}
		}
		for _, c := range x {
			if a, ok := replicateFindAccount(c, depth+1); ok {
				return a, true
			}
		}
	case []any:
		for _, c := range x {
			if a, ok := replicateFindAccount(c, depth+1); ok {
				return a, true
			}
		}
	}
	return replicateAccount{}, false
}

var replicateAmount = regexp.MustCompile(`^\d+(?:\.\d+)?$`)

// replicateMoney reads Replicate's decimal-string dollar amounts.
func replicateMoney(s string) (float64, bool) {
	s = strings.TrimSpace(s)
	if !replicateAmount.MatchString(s) {
		return 0, false
	}
	v, err := strconv.ParseFloat(s, 64)
	return v, err == nil
}
