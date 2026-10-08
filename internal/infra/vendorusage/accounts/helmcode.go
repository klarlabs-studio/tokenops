package accounts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// readerHelmcode registers the reader (readers_gen.go).
func readerHelmcode() usage.Reader { return Helmcode{} }

// Helmcode reads the per-model token quotas Helmcode Cloud's dashboard
// shows, ported from CodexBar
// (Sources/CodexBarCore/Resources/Plugins/helmcode.ts, Providers/Helmcode,
// docs/helmcode.md): GET cloud-api.helmcode.com/api/usage/quota with the
// dashboard session's Cookie header, pasted at setup. Inference API keys
// read no quota.
//
// Each model with a cap is a window named by the model and its length
// ("glm-5 month", "glm-5 5h"), the monthly ones resetting at their period
// end; rolling tiers count only when /api/billing reports a premium
// subscription, as CodexBar does. The prepaid balance
// (/api/billing/credits) is in its own currency, dollars when it is USD.
// The NaN Builders tenant CodexBar also reads is not.
type Helmcode struct {
	BaseURL string
	HTTP    *http.Client
}

func (Helmcode) Endpoint() string               { return "helmcode" }
func (Helmcode) Provider() eventschema.Provider { return "helmcode" }
func (Helmcode) Source() string                 { return "helmcode-account" }

func (h Helmcode) Read(ctx context.Context, key string) (usage.Reading, error) {
	cookie := cookieHeader(key)
	if !strings.Contains(cookie, "=") {
		return usage.Reading{}, fmt.Errorf("%w (the Helmcode credential is not a Cookie header)", usage.ErrAuth)
	}
	api := base(h.BaseURL, "https://cloud-api.helmcode.com")
	header := http.Header{
		"Cookie":     {cookie},
		"Origin":     {"https://cloud.helmcode.com"},
		"Referer":    {"https://cloud.helmcode.com/dashboard"},
		"Accept":     {"application/json"},
		"User-Agent": {browserUA},
	}
	get := func(path string, out any) error {
		body, err := doWeb(ctx, h.HTTP, http.MethodGet, api+path, header, nil)
		if err != nil {
			return err
		}
		if err := json.Unmarshal(body, out); err != nil {
			return fmt.Errorf("accounts: GET %s: %w", hostPath(api+path), err)
		}
		return nil
	}
	var quota struct {
		PeriodStart *string `json:"periodStart"`
		Models      *[]struct {
			Model  string  `json:"model"`
			Cap    number  `json:"cap"`
			Used   number  `json:"tokensUsed"`
			Hours  *number `json:"windowHours"`
			Period string  `json:"periodEnd"`
		} `json:"models"`
	}
	if err := get("/api/usage/quota", &quota); err != nil {
		return usage.Reading{}, err
	}
	if quota.PeriodStart == nil || quota.Models == nil {
		return usage.Reading{}, errors.New("accounts: helmcode quota: unrecognised answer")
	}
	var billing struct {
		Subscription *struct {
			Premium bool `json:"premium"`
		} `json:"subscription"`
	}
	premium := get("/api/billing", &billing) == nil && billing.Subscription != nil && billing.Subscription.Premium
	nextMonth := time.Time{}
	if t, err := time.Parse("2006-01-02", (*quota.PeriodStart + "          ")[:10]); err == nil {
		nextMonth = time.Date(t.Year(), t.Month()+1, 1, 0, 0, 0, 0, time.UTC)
	}
	r := usage.Reading{Scope: "account"}
	for _, m := range *quota.Models {
		name := strings.TrimSpace(m.Model)
		if name == "" || !m.Cap.ok || m.Cap.v <= 0 || (m.Hours != nil && m.Hours.ok && !premium) {
			continue
		}
		w := usage.Window{UsedPct: clampPct(pct(max(0, m.Used.v), m.Cap.v)), ResetsAt: parseTime(m.Period)}
		if m.Hours != nil && m.Hours.ok && m.Hours.v >= 1 {
			w.Duration = time.Duration(m.Hours.v) * time.Hour
			w.Name = name + " " + windowName(w.Duration)
		} else {
			w.Name = name + " month"
			if w.ResetsAt.IsZero() {
				w.ResetsAt = nextMonth
			}
		}
		r.Windows = append(r.Windows, w)
	}
	sort.SliceStable(r.Windows, func(i, j int) bool { return r.Windows[i].UsedPct > r.Windows[j].UsedPct })
	r.Subscription = len(r.Windows) > 0
	var credits struct {
		Balance  *number `json:"balanceMicros"`
		Currency string  `json:"currency"`
	}
	if get("/api/billing/credits", &credits) == nil && credits.Balance != nil && credits.Balance.ok {
		cur := strings.ToUpper(strings.TrimSpace(credits.Currency))
		if cur == "" {
			cur = "EUR"
		}
		bal := max(0, credits.Balance.v/1e6)
		if cur == "USD" {
			r.BalanceUSD, r.HasBalance = bal, true
		} else {
			r.Credits, r.CreditsUnit, r.HasCredits = bal, cur, true
		}
	}
	return r, nil
}
