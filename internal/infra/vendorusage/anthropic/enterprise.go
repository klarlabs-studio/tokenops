package anthropic

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/anthropic"
	"go.klarlabs.de/tokenops/internal/infra/vendorusage/errbody"
)

// ErrMissingAnalyticsKey is returned before any call without a key.
var ErrMissingAnalyticsKey = errors.New("anthropic: no Claude Enterprise Analytics API key")

// AnalyticsClient reads the Claude Enterprise Analytics API
// (https://platform.claude.com/docs/en/manage-claude/analytics-api) with
// an Analytics API key (scope read:analytics). Only Claude Enterprise
// organisations have one.
type AnalyticsClient struct {
	BaseURL    string
	Key        string
	APIVersion string
	HTTPClient *http.Client
}

// enterprisePages bounds the pages followed for one day: a page holds up
// to 1000 users.
const enterprisePages = 200

// ClaudeCodeDay calls GET /v1/organizations/analytics/users for one UTC
// day and sums every user's Claude Code metrics into the organisation's.
// Users without Claude Code activity that day are not counted. The
// report carries no cost or tokens, so Models stays empty. A day not yet
// published returns usage.ErrDayNotAvailable.
func (c *AnalyticsClient) ClaudeCodeDay(ctx context.Context, day time.Time) (usage.ClaudeCodeDay, error) {
	d := day.UTC().Format("2006-01-02")
	out := usage.ClaudeCodeDay{Day: d}
	if c.Key == "" {
		return out, ErrMissingAnalyticsKey
	}
	base, version, hc := c.BaseURL, c.APIVersion, c.HTTPClient
	if base == "" {
		base = "https://api.anthropic.com"
	}
	if version == "" {
		version = "2023-06-01"
	}
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
	}
	q := url.Values{}
	q.Set("date", d)
	q.Set("limit", "1000")
	seen := map[string]bool{}
	for i := 0; ; i++ {
		if i == enterprisePages {
			return out, errors.New("anthropic analytics: users: too many pages")
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/v1/organizations/analytics/users?"+q.Encode(), nil)
		if err != nil {
			return out, fmt.Errorf("anthropic analytics: build request: %w", err)
		}
		// The guide names x-api-key and the reference a bearer token;
		// both carry the same key.
		req.Header.Set("x-api-key", c.Key)
		req.Header.Set("Authorization", "Bearer "+c.Key)
		req.Header.Set("anthropic-version", version)
		req.Header.Set("Accept", "application/json")
		var page enterprisePage
		if err := getAnalytics(hc, req, &page); err != nil {
			return out, err
		}
		for _, r := range page.Data {
			r.addTo(&out)
		}
		if page.NextPage == nil || *page.NextPage == "" {
			return out, nil
		}
		if seen[*page.NextPage] {
			return out, errors.New("anthropic analytics: users: pagination cursor repeated")
		}
		seen[*page.NextPage] = true
		q.Set("page", *page.NextPage)
	}
}

type enterprisePage struct {
	Data     []enterpriseRecord `json:"data"`
	NextPage *string            `json:"next_page"`
}

type enterpriseRecord struct {
	ClaudeCode struct {
		Core struct {
			Commits      int64  `json:"commit_count"`
			Sessions     *int64 `json:"distinct_session_count"`
			PullRequests int64  `json:"pull_request_count"`
			Lines        struct {
				Added   int64 `json:"added_count"`
				Removed int64 `json:"removed_count"`
			} `json:"lines_of_code"`
		} `json:"core_metrics"`
		Tools map[string]struct {
			Accepted int64 `json:"accepted_count"`
			Rejected int64 `json:"rejected_count"`
		} `json:"tool_actions"`
	} `json:"claude_code_metrics"`
}

// addTo sums one user's Claude Code metrics into the day, counting the
// user only when they used Claude Code.
func (r enterpriseRecord) addTo(out *usage.ClaudeCodeDay) {
	cc := r.ClaudeCode
	one := usage.ClaudeCodeDay{Commits: cc.Core.Commits, PullRequests: cc.Core.PullRequests,
		LinesAdded: cc.Core.Lines.Added, LinesRemoved: cc.Core.Lines.Removed}
	if cc.Core.Sessions != nil {
		one.Sessions = *cc.Core.Sessions
	}
	active := one.Sessions > 0 || one.Commits > 0 || one.PullRequests > 0 || one.LinesAdded > 0 || one.LinesRemoved > 0
	for k, v := range cc.Tools {
		if v.Accepted == 0 && v.Rejected == 0 {
			continue
		}
		if one.Tools == nil {
			one.Tools = map[string]usage.ToolDecisions{}
		}
		one.Tools[k] = usage.ToolDecisions{Accepted: v.Accepted, Rejected: v.Rejected}
		active = true
	}
	if !active {
		return
	}
	one.Users = 1
	out.Add(one)
}

// getAnalytics decodes a 2xx response; a 400 naming the most recent
// available day is usage.ErrDayNotAvailable.
func getAnalytics(hc *http.Client, req *http.Request, into any) error {
	resp, err := hc.Do(req)
	if err != nil {
		return fmt.Errorf("anthropic analytics: do request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body := errbody.Read(resp.Body)
		if resp.StatusCode == http.StatusBadRequest && strings.Contains(strings.ToLower(string(body)), "available") {
			return fmt.Errorf("anthropic analytics: %w: %s", usage.ErrDayNotAvailable, errbody.Status(resp.StatusCode, body))
		}
		return fmt.Errorf("anthropic analytics: users: %s", errbody.Status(resp.StatusCode, body))
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<20)).Decode(into); err != nil {
		return fmt.Errorf("anthropic analytics: decode users: %w", err)
	}
	return nil
}
