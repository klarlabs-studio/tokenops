package accounts

import (
	"context"
	"net/http"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// readerSynthetic registers the reader (readers_gen.go).
func readerSynthetic() usage.Reader { return Synthetic{} }

// Synthetic reads the subscription's request quota from GET /v2/quotas
// (https://dev.synthetic.new/docs/synthetic/quotas). Asking does not
// count against the quota.
type Synthetic struct {
	BaseURL string
	HTTP    *http.Client
}

func (Synthetic) Endpoint() string               { return "synthetic" }
func (Synthetic) Provider() eventschema.Provider { return "synthetic" }
func (Synthetic) Source() string                 { return "synthetic-account" }

func (s Synthetic) Read(ctx context.Context, key string) (usage.Reading, error) {
	var resp struct {
		Subscription *struct {
			Limit    number `json:"limit"`
			Requests number `json:"requests"`
			RenewsAt string `json:"renewsAt"`
		} `json:"subscription"`
	}
	if err := getJSON(ctx, s.HTTP, base(s.BaseURL, "https://api.synthetic.new")+"/v2/quotas", key, &resp); err != nil {
		return usage.Reading{}, err
	}
	sub := resp.Subscription
	if sub == nil || !sub.Limit.ok || sub.Limit.v <= 0 {
		return usage.Reading{Scope: "account"}, nil
	}
	return usage.Reading{Scope: "account", Subscription: true, Windows: []usage.Window{{
		Name: "requests", UsedPct: pct(sub.Requests.v, sub.Limit.v), ResetsAt: parseTime(sub.RenewsAt),
	}}}, nil
}
