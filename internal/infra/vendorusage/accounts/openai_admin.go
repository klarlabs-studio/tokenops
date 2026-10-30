package accounts

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// readerOpenAIAdmin registers the reader (readers_gen.go).
func readerOpenAIAdmin() usage.Reader { return OpenAIAdmin{} }

// OpenAIAdmin reads the organisation's API spend this month from
// GET /v1/organization/costs (OpenAI's Administration API, documented at
// platform.openai.com/docs/api-reference/usage/costs), with an organisation
// admin key, as CodexBar's OpenAI API provider does. It is API Platform
// spend, not a ChatGPT plan's windows: those come from the Codex sources.
//
// Its endpoint is its own, "openai-admin": only an admin key stored by
// `tokenops vendor-usage setup openai` or found in OPENAI_ADMIN_KEY is sent
// here, never the API keys the harnesses use.
type OpenAIAdmin struct {
	BaseURL string
	HTTP    *http.Client
	// Now is the clock; nil uses time.Now.
	Now func() time.Time
}

func (OpenAIAdmin) Endpoint() string               { return "openai-admin" }
func (OpenAIAdmin) Provider() eventschema.Provider { return eventschema.ProviderOpenAI }
func (OpenAIAdmin) Source() string                 { return "openai-admin" }

// openAICostPages bounds the pages followed for one month, as CodexBar
// does: a month of daily buckets fits in one.
const openAICostPages = 100

func (o OpenAIAdmin) Read(ctx context.Context, key string) (usage.Reading, error) {
	now := time.Now
	if o.Now != nil {
		now = o.Now
	}
	t := now().UTC()
	start := time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(t.Year(), t.Month(), t.Day()+1, 0, 0, 0, 0, time.UTC)
	days, err := o.DailyCost(ctx, key, start, end)
	if err != nil {
		return usage.Reading{}, err
	}
	var total float64
	for _, v := range days {
		total += v
	}
	// Spend this month with no cap: the Administration API reports none.
	return usage.Reading{Scope: "organization", UsedUSD: total, HasUsed: true}, nil
}

// DailyCost reads what the organisation was billed on each UTC day from
// start to end, in USD, keyed "2006-01-02": the costs endpoint's daily
// buckets, which OpenAI says reconcile to the invoice. A day with no
// costs is 0.
func (o OpenAIAdmin) DailyCost(ctx context.Context, key string, start, end time.Time) (map[string]float64, error) {
	q := url.Values{}
	q.Set("start_time", strconv.FormatInt(start.UTC().Unix(), 10))
	q.Set("end_time", strconv.FormatInt(end.UTC().Unix(), 10))
	q.Set("bucket_width", "1d")
	q.Set("limit", "31")
	root := base(o.BaseURL, "https://api.openai.com") + "/v1/organization/costs?"

	out := map[string]float64{}
	seen := map[string]bool{}
	page := ""
	for i := 0; ; i++ {
		if i == openAICostPages {
			return nil, errors.New("accounts: OpenAI costs: too many pages")
		}
		if page != "" {
			q.Set("page", page)
		}
		var resp struct {
			Data []struct {
				StartTime int64 `json:"start_time"`
				Results   []struct {
					Amount *struct {
						Value    number `json:"value"`
						Currency string `json:"currency"`
					} `json:"amount"`
				} `json:"results"`
			} `json:"data"`
			HasMore  bool   `json:"has_more"`
			NextPage string `json:"next_page"`
		}
		if err := getJSON(ctx, o.HTTP, root+q.Encode(), key, &resp); err != nil {
			return nil, err
		}
		for _, b := range resp.Data {
			day := time.Unix(b.StartTime, 0).UTC().Format("2006-01-02")
			if _, ok := out[day]; !ok {
				out[day] = 0
			}
			for _, r := range b.Results {
				if r.Amount == nil || !r.Amount.Value.ok {
					continue
				}
				if c := strings.ToLower(r.Amount.Currency); c != "" && c != "usd" {
					return nil, fmt.Errorf("accounts: OpenAI costs in %q, not USD", r.Amount.Currency)
				}
				out[day] += r.Amount.Value.v
			}
		}
		if !resp.HasMore {
			return out, nil
		}
		page = strings.TrimSpace(resp.NextPage)
		if page == "" || seen[page] {
			return nil, errors.New("accounts: OpenAI costs: pagination cursor missing or repeated")
		}
		seen[page] = true
	}
}
