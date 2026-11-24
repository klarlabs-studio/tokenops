package accounts

import (
	"context"
	"errors"
	"net/http"
	"time"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// readerSynthetic registers the reader (readers_gen.go).
func readerSynthetic() usage.Reader { return Synthetic{} }

// Synthetic reads the subscription's request quota from GET /v2/quotas
// (https://dev.synthetic.new/docs/synthetic/quotas). Asking does not
// count against the quota. The reset comes from renewsAt (the docs'
// spelling) or renewAt (seen in observed responses); with neither, the
// window is kept and its reset stays unknown.
//
// Current plans report a weekly credit limit, planWeeklyLimit (at the root
// or under data), and retire the subscription counter: when it is there,
// it is the "week" window and subscription is not read (CodexBar
// a20e95e696, Sources/CodexBarCore/Resources/Plugins/synthetic.js). An
// answer with neither is an error, never an account that reads as nothing.
type Synthetic struct {
	BaseURL string
	HTTP    *http.Client
}

func (Synthetic) Endpoint() string               { return "synthetic" }
func (Synthetic) Provider() eventschema.Provider { return "synthetic" }
func (Synthetic) Source() string                 { return "synthetic-account" }

// syntheticQuotas is the quota answer, at the root or under data.
type syntheticQuotas struct {
	Subscription *struct {
		Limit    number `json:"limit"`
		Requests number `json:"requests"`
		RenewsAt string `json:"renewsAt"`
		RenewAt  string `json:"renewAt"`
	} `json:"subscription"`
	PlanWeekly *struct {
		PercentRemaining *number `json:"percentRemaining"`
		PercentUsed      *number `json:"percentUsed"`
		NextRegenAt      string  `json:"nextRegenAt"`
		ResetAt          string  `json:"resetAt"`
		RenewsAt         string  `json:"renewsAt"`
	} `json:"planWeeklyLimit"`
}

func (s Synthetic) Read(ctx context.Context, key string) (usage.Reading, error) {
	var resp struct {
		syntheticQuotas
		Data *syntheticQuotas `json:"data"`
	}
	if err := getJSON(ctx, s.HTTP, base(s.BaseURL, "https://api.synthetic.new")+"/v2/quotas", key, &resp); err != nil {
		return usage.Reading{}, err
	}
	q := resp.syntheticQuotas
	if q.PlanWeekly == nil && q.Subscription == nil && resp.Data != nil {
		q = *resp.Data
	}
	if w := q.PlanWeekly; w != nil {
		// Percentage points, 0–100, even when less than 1% remains.
		var used float64
		switch {
		case w.PercentUsed != nil && w.PercentUsed.ok:
			used = w.PercentUsed.v
		case w.PercentRemaining != nil && w.PercentRemaining.ok:
			used = 100 - w.PercentRemaining.v
		default:
			return usage.Reading{}, errors.New("accounts: synthetic quotas: a weekly plan limit with no percentage")
		}
		reset := w.NextRegenAt
		for _, alt := range []string{w.ResetAt, w.RenewsAt} {
			if reset == "" {
				reset = alt
			}
		}
		return usage.Reading{Scope: "account", Subscription: true, Windows: []usage.Window{{
			Name: "week", UsedPct: clampPct(used), Duration: 7 * 24 * time.Hour, ResetsAt: parseTime(reset),
		}}}, nil
	}
	sub := q.Subscription
	if sub == nil || !sub.Limit.ok || sub.Limit.v <= 0 || !sub.Requests.ok {
		return usage.Reading{}, errors.New("accounts: synthetic quotas: no quota in the answer")
	}
	return usage.Reading{Scope: "account", Subscription: true, Windows: []usage.Window{{
		Name: "requests", UsedPct: pct(sub.Requests.v, sub.Limit.v), ResetsAt: parseTime(renewal(sub.RenewsAt, sub.RenewAt)),
	}}}, nil
}

// renewal is the first non-empty of the two spellings the quota API uses.
func renewal(renewsAt, renewAt string) string {
	if renewsAt != "" {
		return renewsAt
	}
	return renewAt
}
