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
)

// claudeCodePages bounds the pages followed for one day: a page holds up
// to 1000 users.
const claudeCodePages = 100

// ClaudeCodeDay calls GET /v1/organizations/usage_report/claude_code for
// one UTC day and sums its per-user records into the organisation's
// (usage.ClaudeCodeDay). E-mail addresses and API key names are read
// only to tell users from API keys and are not kept.
func (c *AdminClient) ClaudeCodeDay(ctx context.Context, day time.Time) (usage.ClaudeCodeDay, error) {
	d := day.UTC().Format("2006-01-02")
	out := usage.ClaudeCodeDay{Day: d}
	if c.AdminKey == "" {
		return out, usage.ErrMissingAdminKey
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
	q.Set("starting_at", d)
	q.Set("limit", "1000")
	seen := map[string]bool{}
	for i := 0; ; i++ {
		if i == claudeCodePages {
			return out, errors.New("anthropic admin: claude_code report: too many pages")
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/v1/organizations/usage_report/claude_code?"+q.Encode(), nil)
		if err != nil {
			return out, fmt.Errorf("anthropic admin: build request: %w", err)
		}
		req.Header.Set("x-api-key", c.AdminKey)
		req.Header.Set("anthropic-version", version)
		req.Header.Set("Accept", "application/json")
		var page claudeCodePage
		if err := getCost(hc, req, &page); err != nil {
			return out, err
		}
		for _, r := range page.Data {
			if err := r.addTo(&out); err != nil {
				return out, err
			}
		}
		if !page.HasMore {
			return out, nil
		}
		if page.NextPage == nil || *page.NextPage == "" || seen[*page.NextPage] {
			return out, errors.New("anthropic admin: claude_code report: pagination cursor missing or repeated")
		}
		seen[*page.NextPage] = true
		q.Set("page", *page.NextPage)
	}
}

type claudeCodePage struct {
	Data     []claudeCodeRecord `json:"data"`
	HasMore  bool               `json:"has_more"`
	NextPage *string            `json:"next_page"`
}

type claudeCodeRecord struct {
	Actor struct {
		Type string `json:"type"`
	} `json:"actor"`
	CoreMetrics struct {
		NumSessions int64 `json:"num_sessions"`
		LinesOfCode struct {
			Added   int64 `json:"added"`
			Removed int64 `json:"removed"`
		} `json:"lines_of_code"`
		Commits      int64 `json:"commits_by_claude_code"`
		PullRequests int64 `json:"pull_requests_by_claude_code"`
	} `json:"core_metrics"`
	ToolActions    map[string]usage.ToolDecisions `json:"tool_actions"`
	ModelBreakdown []struct {
		Model  string `json:"model"`
		Tokens struct {
			Input         int64 `json:"input"`
			Output        int64 `json:"output"`
			CacheRead     int64 `json:"cache_read"`
			CacheCreation int64 `json:"cache_creation"`
		} `json:"tokens"`
		EstimatedCost struct {
			Currency string      `json:"currency"`
			Amount   json.Number `json:"amount"`
		} `json:"estimated_cost"`
	} `json:"model_breakdown"`
}

// addTo sums one user's record into the day.
func (r claudeCodeRecord) addTo(out *usage.ClaudeCodeDay) error {
	one := usage.ClaudeCodeDay{Sessions: r.CoreMetrics.NumSessions, LinesAdded: r.CoreMetrics.LinesOfCode.Added,
		LinesRemoved: r.CoreMetrics.LinesOfCode.Removed, Commits: r.CoreMetrics.Commits, PullRequests: r.CoreMetrics.PullRequests,
		Tools: r.ToolActions, Models: map[string]usage.ClaudeCodeModel{}}
	switch r.Actor.Type {
	case "api_actor":
		one.APIKeys = 1
	default:
		one.Users = 1
	}
	for _, m := range r.ModelBreakdown {
		if cur := strings.ToUpper(m.EstimatedCost.Currency); cur != "" && cur != "USD" {
			return fmt.Errorf("anthropic admin: claude_code cost in %q, not USD", m.EstimatedCost.Currency)
		}
		cents := 0.0
		if s := m.EstimatedCost.Amount.String(); s != "" {
			v, err := strconv.ParseFloat(s, 64)
			if err != nil {
				return fmt.Errorf("anthropic admin: claude_code cost %q: %w", s, err)
			}
			cents = v
		}
		x := one.Models[m.Model]
		x.InputTokens += m.Tokens.Input
		x.OutputTokens += m.Tokens.Output
		x.CacheReadTokens += m.Tokens.CacheRead
		x.CacheCreationTokens += m.Tokens.CacheCreation
		x.EstimatedUSD += cents / 100
		one.Models[m.Model] = x
	}
	out.Add(one)
	return nil
}
