package accounts

import (
	"context"
	"errors"
	"net/http"
	"time"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// readerClinePass registers the reader (readers_gen.go).
func readerClinePass() usage.Reader { return ClinePass{} }

// ClinePass reads the subscription's 5-hour, weekly and monthly windows
// from GET /api/v1/users/me/plan/usage-limits, the endpoint CodexBar's
// ClinePass provider calls. percentUsed is already a percentage. The
// endpoint reports no pay-as-you-go balance.
type ClinePass struct {
	BaseURL string
	HTTP    *http.Client
}

func (ClinePass) Endpoint() string               { return "clinepass" }
func (ClinePass) Provider() eventschema.Provider { return "clinepass" }
func (ClinePass) Source() string                 { return "clinepass-account" }

func (c ClinePass) Read(ctx context.Context, key string) (usage.Reading, error) {
	var resp struct {
		Success *bool `json:"success"`
		Data    *struct {
			Limits []struct {
				Type        string `json:"type"`
				PercentUsed number `json:"percentUsed"`
				ResetsAt    string `json:"resetsAt"`
			} `json:"limits"`
		} `json:"data"`
	}
	if err := getJSON(ctx, c.HTTP, base(c.BaseURL, "https://api.cline.bot")+"/api/v1/users/me/plan/usage-limits", key, &resp); err != nil {
		return usage.Reading{}, err
	}
	if resp.Success == nil || !*resp.Success || resp.Data == nil {
		return usage.Reading{}, errors.New("accounts: GET api.cline.bot/api/v1/users/me/plan/usage-limits: unsuccessful answer")
	}
	lengths := map[string]time.Duration{
		"five_hour": 5 * time.Hour,
		"weekly":    7 * 24 * time.Hour,
		"monthly":   30 * 24 * time.Hour,
	}
	r := usage.Reading{Scope: "account"}
	for _, l := range resp.Data.Limits {
		d, known := lengths[l.Type]
		if !known || !l.PercentUsed.ok {
			continue
		}
		r.Windows = append(r.Windows, usage.Window{Name: windowName(d), UsedPct: clampPct(l.PercentUsed.v), Duration: d, ResetsAt: parseTime(l.ResetsAt)})
	}
	r.Subscription = len(r.Windows) > 0
	return r, nil
}
