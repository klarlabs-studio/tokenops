package anthropic

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/anthropic"
	"go.klarlabs.de/tokenops/internal/infra/vendorusage/errbody"
)

// costPages bounds the pages followed for one report: a page holds up to
// 31 daily buckets.
const costPages = 100

// DailyCost calls GET /v1/organizations/cost_report and returns what the
// organisation was billed on each UTC day from start to end, in USD,
// keyed "2006-01-02" (ADR 0003). The report states amounts in cents as
// decimal strings; a day with no costs is 0. A Claude Console admin key
// is required: a Claude Enterprise key reaches the Analytics API instead.
func (c *AdminClient) DailyCost(ctx context.Context, start, end time.Time) (map[string]float64, error) {
	if c.AdminKey == "" {
		return nil, usage.ErrMissingAdminKey
	}
	base := c.BaseURL
	if base == "" {
		base = "https://api.anthropic.com"
	}
	version := c.APIVersion
	if version == "" {
		version = "2023-06-01"
	}
	hc := c.HTTPClient
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
	}
	q := url.Values{}
	q.Set("starting_at", start.UTC().Format(time.RFC3339))
	q.Set("ending_at", end.UTC().Format(time.RFC3339))
	q.Set("bucket_width", "1d")
	q.Set("limit", "31")
	out := map[string]float64{}
	seen := map[string]bool{}
	for i := 0; ; i++ {
		if i == costPages {
			return nil, errors.New("anthropic admin: cost_report: too many pages")
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/v1/organizations/cost_report?"+q.Encode(), nil)
		if err != nil {
			return nil, fmt.Errorf("anthropic admin: build request: %w", err)
		}
		req.Header.Set("x-api-key", c.AdminKey)
		req.Header.Set("anthropic-version", version)
		req.Header.Set("Accept", "application/json")
		var page struct {
			Data []struct {
				StartingAt time.Time `json:"starting_at"`
				Results    []struct {
					Amount   string `json:"amount"`
					Currency string `json:"currency"`
				} `json:"results"`
			} `json:"data"`
			HasMore  bool    `json:"has_more"`
			NextPage *string `json:"next_page"`
		}
		if err := getCost(hc, req, &page); err != nil {
			return nil, err
		}
		for _, b := range page.Data {
			day := b.StartingAt.UTC().Format("2006-01-02")
			if _, ok := out[day]; !ok {
				out[day] = 0
			}
			for _, r := range b.Results {
				if cur := strings.ToUpper(r.Currency); cur != "" && cur != "USD" {
					return nil, fmt.Errorf("anthropic admin: cost_report in %q, not USD", r.Currency)
				}
				cents, err := strconv.ParseFloat(strings.TrimSpace(r.Amount), 64)
				if err != nil {
					return nil, fmt.Errorf("anthropic admin: cost_report amount %q: %w", r.Amount, err)
				}
				out[day] += cents / 100
			}
		}
		if !page.HasMore {
			return out, nil
		}
		if page.NextPage == nil || *page.NextPage == "" || seen[*page.NextPage] {
			return nil, errors.New("anthropic admin: cost_report: pagination cursor missing or repeated")
		}
		seen[*page.NextPage] = true
		q.Set("page", *page.NextPage)
	}
}

func getCost(hc *http.Client, req *http.Request, into any) error {
	resp, err := hc.Do(req)
	if err != nil {
		return fmt.Errorf("anthropic admin: do request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("anthropic admin: cost_report: %s", errbody.Status(resp.StatusCode, errbody.Read(resp.Body)))
	}
	if err := json.NewDecoder(resp.Body).Decode(into); err != nil {
		return fmt.Errorf("anthropic admin: decode cost_report: %w", err)
	}
	return nil
}
