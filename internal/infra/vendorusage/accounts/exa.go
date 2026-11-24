package accounts

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// readerExa registers the reader (readers_gen.go).
func readerExa() usage.Reader { return Exa{} }

// Exa reads one API key's spend this UTC month from Exa's Team Management
// API, GET admin-api.exa.ai/team-management/api-keys/{id}/usage
// (exa.ai/docs/reference/team-management/get-api-key-usage), ported from
// CodexBar (Sources/CodexBarCore/Resources/Plugins/exa.js, Providers/Exa,
// docs/exa.md). It needs a team service key, sent in x-api-key, and the
// ID of the key whose spend is read: the credential is
// API_KEY_ID:SERVICE_KEY. Ordinary search keys cannot read it. It is that
// key's spend, not the team's, and not a balance.
type Exa struct {
	BaseURL string
	HTTP    *http.Client
	// Now is the clock; nil uses time.Now.
	Now func() time.Time
}

func (Exa) Endpoint() string               { return "exa" }
func (Exa) Provider() eventschema.Provider { return "exa" }
func (Exa) Source() string                 { return "exa-key" }

func (e Exa) Read(ctx context.Context, credential string) (usage.Reading, error) {
	id, key, ok := strings.Cut(strings.TrimSpace(credential), ":")
	id, key = strings.TrimSpace(id), strings.TrimSpace(key)
	if !ok || id == "" || key == "" || id == "." || id == ".." {
		return usage.Reading{}, fmt.Errorf("%w (Exa needs API_KEY_ID:SERVICE_KEY)", usage.ErrAuth)
	}
	now := time.Now
	if e.Now != nil {
		now = e.Now
	}
	end := now().UTC().Truncate(time.Second)
	start := time.Date(end.Year(), end.Month(), 1, 0, 0, 0, 0, time.UTC)
	q := url.Values{}
	q.Set("start_date", start.Format("2006-01-02T15:04:05.000Z"))
	q.Set("end_date", end.Format("2006-01-02T15:04:05.000Z"))
	u := base(e.BaseURL, "https://admin-api.exa.ai") + "/team-management/api-keys/" + url.PathEscape(id) + "/usage?" + q.Encode()
	var resp struct {
		Total  *float64 `json:"total_cost_usd"`
		Period *struct {
			Start string `json:"start"`
			End   string `json:"end"`
		} `json:"period"`
	}
	if err := getJSONHeader(ctx, e.HTTP, u, "x-api-key", key, &resp); err != nil {
		return usage.Reading{}, err
	}
	// The answer must be for the period asked: a spend over another one
	// is not this month's.
	if resp.Total == nil || *resp.Total < 0 || resp.Period == nil ||
		!parseTime(resp.Period.Start).Equal(start) || !parseTime(resp.Period.End).Equal(end) {
		return usage.Reading{}, errors.New("accounts: exa api key usage: unrecognised answer")
	}
	return usage.Reading{Scope: "key", UsedUSD: *resp.Total, HasUsed: true}, nil
}
