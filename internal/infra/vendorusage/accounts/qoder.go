package accounts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// readerQoder registers the reader (readers_gen.go).
func readerQoder() usage.Reader { return Qoder{} }

// Qoder reads Qoder's big-model credits used against the plan's total
// (with a team's shared credits added), until the next reset, from the
// account dashboard's API with its browser session, as CodexBar's Qoder
// plugin does (Sources/CodexBarCore/Resources/Plugins/qoder.js). The
// international site is asked first and the China mainland site
// (qoder.com.cn) when it refuses the session.
type Qoder struct {
	// Sites replace https://qoder.com and https://qoder.com.cn in tests.
	Sites []string
	HTTP  *http.Client
}

func (Qoder) Endpoint() string               { return "qoder" }
func (Qoder) Provider() eventschema.Provider { return "qoder" }
func (Qoder) Source() string                 { return "qoder-web" }

type qoderSummary struct {
	Used      *float64 `json:"usedValue"`
	Limit     *float64 `json:"limitValue"`
	Remaining *float64 `json:"remainingValue"`
	Percent   *float64 `json:"usagePercentage"`
	UsedS     *float64 `json:"used_value"`
	LimitS    *float64 `json:"limit_value"`
	RemainS   *float64 `json:"remaining_value"`
	PercentS  *float64 `json:"usage_percentage"`
}

type qoderQuota struct {
	Summary  *qoderSummary `json:"quotaSummary"`
	SummaryS *qoderSummary `json:"quota_summary"`
}

func (q Qoder) Read(ctx context.Context, cookie string) (usage.Reading, error) {
	if !isSession(cookie) {
		return usage.Reading{}, fmt.Errorf("%w (Qoder is read with the dashboard's Cookie header)", usage.ErrAuth)
	}
	sites := q.Sites
	if len(sites) == 0 {
		sites = []string{"https://qoder.com", "https://qoder.com.cn"}
	}
	var err error
	for _, site := range sites {
		var body []byte
		body, err = doWeb(ctx, q.HTTP, http.MethodGet, site+"/api/v2/me/usages/big_model_credits", http.Header{
			"Cookie": {cookie}, "Accept": {"application/json, text/plain, */*"}, "Accept-Language": {"en-US,en;q=0.9"},
			"Origin": {site}, "Referer": {site + "/account/usage"}, "X-Requested-With": {"XMLHttpRequest"}, "Bx-V": {"2.5.35"},
		}, nil)
		if errors.Is(err, usage.ErrAuth) {
			continue
		}
		if err != nil {
			return usage.Reading{}, err
		}
		return parseQoder(body)
	}
	return usage.Reading{}, err
}

func parseQoder(body []byte) (usage.Reading, error) {
	var root struct {
		Total      *qoderQuota `json:"totalQuota"`
		TotalS     *qoderQuota `json:"total_quota"`
		Shared     *qoderQuota `json:"sharedQuota"`
		SharedS    *qoderQuota `json:"shared_quota"`
		NextReset  stamp       `json:"nextResetAt"`
		NextResetS stamp       `json:"next_reset_at"`
	}
	if err := json.Unmarshal(body, &root); err != nil {
		return usage.Reading{}, fmt.Errorf("accounts: qoder: %w", err)
	}
	summary := func(a, b *qoderQuota) *qoderSummary {
		for _, q := range []*qoderQuota{a, b} {
			if q == nil {
				continue
			}
			for _, s := range []*qoderSummary{q.Summary, q.SummaryS} {
				if s != nil {
					return s
				}
			}
		}
		return nil
	}
	pick := func(x, y *float64) (float64, bool) {
		if x != nil {
			return *x, true
		}
		if y != nil {
			return *y, true
		}
		return 0, false
	}
	base := summary(root.Total, root.TotalS)
	if base == nil {
		return usage.Reading{}, errors.New("accounts: qoder: no quota summary in the answer")
	}
	used, okU := pick(base.Used, base.UsedS)
	total, okT := pick(base.Limit, base.LimitS)
	if !okU || !okT || used < 0 || total < 0 {
		return usage.Reading{}, errors.New("accounts: qoder: invalid quota figures")
	}
	provided, hasProvided := pick(base.Percent, base.PercentS)
	if shared := summary(root.Shared, root.SharedS); shared != nil {
		su, _ := pick(shared.Used, shared.UsedS)
		st, _ := pick(shared.Limit, shared.LimitS)
		used, total, hasProvided = used+su, total+st, false
	}
	share := 100.0
	switch {
	case hasProvided:
		share = provided
	case total > 0:
		share = pct(used, total)
	}
	reset := root.NextReset.t
	if reset.IsZero() {
		reset = root.NextResetS.t
	}
	return usage.Reading{Scope: "account", Subscription: true, Windows: []usage.Window{
		{Name: "credits", UsedPct: clampPct(share), ResetsAt: reset},
	}}, nil
}
