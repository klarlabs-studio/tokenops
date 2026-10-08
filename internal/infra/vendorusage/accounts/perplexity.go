package accounts

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// readerPerplexity registers the reader (readers_gen.go).
func readerPerplexity() usage.Reader { return Perplexity{} }

// Perplexity reads the credits perplexity.ai's account page shows, ported
// from CodexBar (Sources/CodexBarCore/Resources/Plugins/perplexity.js,
// Providers/Perplexity, docs/perplexity.md): GET
// www.perplexity.ai/rest/billing/credits with the browser's session cookie.
// Perplexity's API keys do not read it, and one is never sent there.
//
// The credit grants are USD cents. Usage is drawn from the plan's
// recurring grant first, as CodexBar models it; the share of that grant
// used is the "month" window, resetting at the renewal date. The balance
// is the account's credit left, in dollars. Promotional and purchased
// grants have no window and are not read.
type Perplexity struct {
	BaseURL string
	HTTP    *http.Client
}

func (Perplexity) Endpoint() string               { return "perplexity" }
func (Perplexity) Provider() eventschema.Provider { return "perplexity" }
func (Perplexity) Source() string                 { return "perplexity-account" }

// perplexitySessionCookies are the names Perplexity's sign-in has used for
// its session, in the order they are tried.
var perplexitySessionCookies = []string{
	"__Secure-next-auth.session-token",
	"__Secure-authjs.session-token",
	"next-auth.session-token",
	"authjs.session-token",
}

type perplexityCredits struct {
	Balance        *number `json:"balance_cents"`
	Renewal        *stamp  `json:"renewal_date_ts"`
	PeriodPurchase *number `json:"current_period_purchased_cents"`
	TotalUsage     *number `json:"total_usage_cents"`
	Grants         *[]struct {
		Type   string `json:"type"`
		Amount number `json:"amount_cents"`
	} `json:"credit_grants"`
}

func (p Perplexity) Read(ctx context.Context, key string) (usage.Reading, error) {
	if strings.HasPrefix(strings.TrimSpace(key), "pplx-") {
		// An API key is not a web session; it is refused without a call.
		return usage.Reading{}, fmt.Errorf("%w (a Perplexity API key does not read the account's credits)", usage.ErrAuth)
	}
	cookies := perplexityCookies(key)
	if len(cookies) == 0 {
		return usage.Reading{}, fmt.Errorf("%w (no Perplexity session cookie in the credential)", usage.ErrAuth)
	}
	host := base(p.BaseURL, "https://www.perplexity.ai")
	var resp perplexityCredits
	var err error
	for _, cookie := range cookies {
		header := http.Header{
			"Cookie":     {cookie},
			"Origin":     {"https://www.perplexity.ai"},
			"Referer":    {"https://www.perplexity.ai/account/usage"},
			"User-Agent": {browserUA},
		}
		resp = perplexityCredits{}
		err = doJSON(ctx, p.HTTP, http.MethodGet, host+"/rest/billing/credits?version=2.18&source=default", header, nil, &resp)
		if !errors.Is(err, usage.ErrAuth) {
			break
		}
	}
	if err != nil {
		return usage.Reading{}, err
	}
	if resp.Balance == nil || !resp.Balance.ok || resp.Renewal == nil || resp.TotalUsage == nil || !resp.TotalUsage.ok || resp.Grants == nil {
		return usage.Reading{}, errors.New("accounts: perplexity credits: unrecognised answer")
	}
	var recurring float64
	for _, g := range *resp.Grants {
		if g.Type == "recurring" {
			recurring += g.Amount.v
		}
	}
	r := usage.Reading{Scope: "account", BalanceUSD: resp.Balance.v / 100, HasBalance: true}
	if recurring > 0 {
		used := min(max(resp.TotalUsage.v, 0), recurring)
		r.Subscription = true
		r.Windows = []usage.Window{{Name: "month", UsedPct: clampPct(pct(used, recurring)), ResetsAt: resp.Renewal.t}}
	}
	return r, nil
}

// perplexityCookies is the session cookie to send, one candidate per
// request: from a Cookie header, the first known name present (a session
// split into numbered chunks, name.0, name.1, is joined); a bare value is
// tried under each name.
func perplexityCookies(credential string) []string {
	credential = strings.TrimSpace(credential)
	if credential == "" {
		return nil
	}
	if !strings.ContainsAny(credential, "=;") {
		out := make([]string, 0, len(perplexitySessionCookies))
		for _, n := range perplexitySessionCookies {
			out = append(out, n+"="+credential)
		}
		return out
	}
	for _, n := range perplexitySessionCookies {
		if v := cookieValue(credential, n); v != "" {
			return []string{n + "=" + v}
		}
		if v := perplexityChunks(credential, n); v != "" {
			return []string{n + "=" + v}
		}
	}
	return nil
}

// perplexityChunks joins a session cookie split into name.0, name.1, ...,
// or returns "" when the chunks are not a complete run from 0.
func perplexityChunks(header, name string) string {
	chunks := map[int]string{}
	prefix := strings.ToLower(name) + "."
	for part := range strings.SplitSeq(header, ";") {
		k, v, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok || !strings.HasPrefix(strings.ToLower(k), prefix) {
			continue
		}
		i, err := strconv.Atoi(k[len(prefix):])
		if err == nil && i >= 0 && strings.TrimSpace(v) != "" {
			chunks[i] = strings.TrimSpace(v)
		}
	}
	idx := make([]int, 0, len(chunks))
	for i := range chunks {
		idx = append(idx, i)
	}
	sort.Ints(idx)
	var b strings.Builder
	for want, i := range idx {
		if i != want {
			return ""
		}
		b.WriteString(chunks[i])
	}
	return b.String()
}
