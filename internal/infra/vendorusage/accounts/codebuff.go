package accounts

import (
	"context"
	"net/http"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// readerCodebuff registers the reader (readers_gen.go).
func readerCodebuff() usage.Reader { return Codebuff{} }

// Codebuff reads the account's credits from POST /api/v1/usage on
// www.codebuff.com, the endpoint the codebuff CLI and CodexBar's Codebuff
// provider call. Figures are Codebuff credits, not dollars: credits used
// against the quota until the next quota reset. The weekly rate limit
// (/api/user/subscription) answers only the CLI's session token, which is
// another application's sign-in and is not read.
type Codebuff struct {
	BaseURL string
	HTTP    *http.Client
}

func (Codebuff) Endpoint() string               { return "codebuff" }
func (Codebuff) Provider() eventschema.Provider { return "codebuff" }
func (Codebuff) Source() string                 { return "codebuff-account" }

func (c Codebuff) Read(ctx context.Context, key string) (usage.Reading, error) {
	var resp struct {
		Usage            number `json:"usage"`
		Used             number `json:"used"`
		Quota            number `json:"quota"`
		Limit            number `json:"limit"`
		RemainingBalance number `json:"remainingBalance"`
		Remaining        number `json:"remaining"`
		NextQuotaReset   stamp  `json:"next_quota_reset"`
	}
	header := http.Header{"Authorization": {"Bearer " + key}}
	// CodexBar sends a fingerprint; the endpoint asks for one.
	payload := map[string]string{"fingerprintId": "tokenops-usage"}
	if err := postJSON(ctx, c.HTTP, base(c.BaseURL, "https://www.codebuff.com")+"/api/v1/usage", header, payload, &resp); err != nil {
		return usage.Reading{}, err
	}
	used, total, remaining := firstNumber(resp.Usage, resp.Used), firstNumber(resp.Quota, resp.Limit), firstNumber(resp.RemainingBalance, resp.Remaining)
	if !total.ok && used.ok && remaining.ok {
		total = number{v: used.v + remaining.v, ok: true}
	}
	if !used.ok && total.ok && remaining.ok {
		used = number{v: max(0, total.v-remaining.v), ok: true}
	}
	r := usage.Reading{Scope: "account"}
	switch {
	case total.ok && total.v > 0 && used.ok:
		r.Windows = []usage.Window{{Name: "credits", UsedPct: clampPct(pct(used.v, total.v)), ResetsAt: resp.NextQuotaReset.t}}
	case used.ok || remaining.ok:
		// Credits reported with no quota to spend them against: spent.
		r.Windows = []usage.Window{{Name: "credits", UsedPct: 100, ResetsAt: resp.NextQuotaReset.t}}
	}
	r.Subscription = len(r.Windows) > 0
	return r, nil
}

// firstNumber is the first of ns the vendor reported.
func firstNumber(ns ...number) number {
	for _, n := range ns {
		if n.ok {
			return n
		}
	}
	return number{}
}
