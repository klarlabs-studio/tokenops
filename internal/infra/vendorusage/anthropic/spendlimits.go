package anthropic

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"go.klarlabs.de/tokenops/internal/infra/vendorusage/errbody"
)

// The Spend Limits API (https://platform.claude.com/docs/en/manage-claude/spend-limits-api)
// reports each Claude Enterprise member's effective spend limit, where it
// was inherited from, the spend so far in its period, and the members'
// requests for more. It takes the same Enterprise key as the Analytics
// API, created with the read:spend_limits scope. TokenOps only reads it.

// ErrSpendLimitsScope is a key without read:spend_limits.
var ErrSpendLimitsScope = errors.New("the Claude Enterprise key lacks the read:spend_limits scope")

// spendLimitPages bounds the pages followed: a page holds up to 1000
// members.
const spendLimitPages = 200

// SpendLimit is one member's effective limit for one period. Amounts are
// in the minor unit of Currency (cents for USD).
type SpendLimit struct {
	UserID string
	// Name and Email are the member's as Anthropic holds them; empty for
	// an account that is unavailable or deleted.
	Name  string
	Email string
	// Period is daily, weekly or monthly.
	Period   string
	Currency string
	// Limit is nil when no limit applies for the period; 0 is included
	// usage only.
	Limit *float64
	// Spent is the period's spend so far. Anthropic reports 0 while its
	// reading is unavailable.
	Spent float64
	// Source is where the limit was inherited from (user, seat_tier,
	// rbac_group, organization, …), and SourceID names which one: the
	// seat tier or the group.
	Source   string
	SourceID string
}

// IncreaseRequest is a member's pending request for a higher limit.
type IncreaseRequest struct {
	ID        string
	UserID    string
	Name      string
	Email     string
	Period    string
	CreatedAt time.Time
}

type spendActor struct {
	Type   string  `json:"type"`
	UserID string  `json:"user_id"`
	Name   *string `json:"name"`
	Email  *string `json:"email_address"`
}

type spendScope struct {
	Type      string `json:"type"`
	UserID    string `json:"user_id"`
	SeatTier  string `json:"seat_tier"`
	Group     string `json:"rbac_group_id"`
	Service   string `json:"service"`
	Workspace string `json:"workspace_id"`
}

// id names which scope of its type: the seat tier, group, service or
// workspace; a user or the organisation needs no name.
func (s spendScope) id() string {
	for _, v := range []string{s.SeatTier, s.Group, s.Service, s.Workspace} {
		if v != "" {
			return v
		}
	}
	return ""
}

type spendSummary struct {
	Actor    spendActor `json:"actor"`
	Amount   *string    `json:"amount"`
	Currency string     `json:"currency"`
	Period   string     `json:"period"`
	Spent    string     `json:"period_to_date_spend"`
	Source   spendScope `json:"source"`
}

// SpendLimits lists every member's effective limits, one per period a
// limit resolves for. Rows for a scoped API key rather than a member are
// left out.
func (c *AnalyticsClient) SpendLimits(ctx context.Context) ([]SpendLimit, error) {
	var out []SpendLimit
	err := c.pages(ctx, "/v1/organizations/spend_limits/effective", url.Values{}, func(raw json.RawMessage) error {
		var rows []spendSummary
		if err := json.Unmarshal(raw, &rows); err != nil {
			return err
		}
		for _, r := range rows {
			if r.Actor.Type != "user_actor" {
				continue
			}
			l := SpendLimit{UserID: r.Actor.UserID, Name: deref(r.Actor.Name), Email: deref(r.Actor.Email),
				Period: r.Period, Currency: r.Currency, Source: r.Source.Type, SourceID: r.Source.id()}
			if r.Amount != nil {
				v, err := strconv.ParseFloat(*r.Amount, 64)
				if err != nil {
					return fmt.Errorf("amount %q: %w", *r.Amount, err)
				}
				l.Limit = &v
			}
			if r.Spent != "" {
				v, err := strconv.ParseFloat(r.Spent, 64)
				if err != nil {
					return fmt.Errorf("period_to_date_spend %q: %w", r.Spent, err)
				}
				l.Spent = v
			}
			out = append(out, l)
		}
		return nil
	})
	return out, err
}

// PendingIncreaseRequests lists the members' requests for a higher limit
// that wait for an admin, newest first.
func (c *AnalyticsClient) PendingIncreaseRequests(ctx context.Context) ([]IncreaseRequest, error) {
	var out []IncreaseRequest
	q := url.Values{}
	q.Set("status[]", "pending")
	err := c.pages(ctx, "/v1/organizations/spend_limit_increase_requests", q, func(raw json.RawMessage) error {
		var rows []struct {
			ID        string     `json:"id"`
			Actor     spendActor `json:"actor"`
			Period    string     `json:"period"`
			CreatedAt time.Time  `json:"created_at"`
			Status    string     `json:"status"`
		}
		if err := json.Unmarshal(raw, &rows); err != nil {
			return err
		}
		for _, r := range rows {
			if r.Status != "pending" || r.Actor.Type != "user_actor" {
				continue
			}
			out = append(out, IncreaseRequest{ID: r.ID, UserID: r.Actor.UserID, Name: deref(r.Actor.Name),
				Email: deref(r.Actor.Email), Period: r.Period, CreatedAt: r.CreatedAt.UTC()})
		}
		return nil
	})
	return out, err
}

// pages follows a Spend Limits list's cursor, handing each page's data to
// each.
func (c *AnalyticsClient) pages(ctx context.Context, path string, q url.Values, each func(json.RawMessage) error) error {
	if c.Key == "" {
		return ErrMissingAnalyticsKey
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
	q.Set("limit", "1000")
	seen := map[string]bool{}
	for i := 0; ; i++ {
		if i == spendLimitPages {
			return fmt.Errorf("anthropic spend limits: %s: too many pages", path)
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+path+"?"+q.Encode(), nil)
		if err != nil {
			return fmt.Errorf("anthropic spend limits: build request: %w", err)
		}
		req.Header.Set("x-api-key", c.Key)
		req.Header.Set("Authorization", "Bearer "+c.Key)
		req.Header.Set("anthropic-version", version)
		req.Header.Set("Accept", "application/json")
		var page struct {
			Data     json.RawMessage `json:"data"`
			NextPage *string         `json:"next_page"`
		}
		if err := getSpendLimits(hc, req, &page); err != nil {
			return err
		}
		if err := each(page.Data); err != nil {
			return fmt.Errorf("anthropic spend limits: decode %s: %w", path, err)
		}
		if page.NextPage == nil || *page.NextPage == "" {
			return nil
		}
		if seen[*page.NextPage] {
			return fmt.Errorf("anthropic spend limits: %s: pagination cursor repeated", path)
		}
		seen[*page.NextPage] = true
		q.Set("page", *page.NextPage)
	}
}

func getSpendLimits(hc *http.Client, req *http.Request, into any) error {
	resp, err := hc.Do(req)
	if err != nil {
		return fmt.Errorf("anthropic spend limits: do request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusForbidden {
		return fmt.Errorf("anthropic spend limits: %w: %s", ErrSpendLimitsScope, errbody.Status(resp.StatusCode, errbody.Read(resp.Body)))
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("anthropic spend limits: %s", errbody.Status(resp.StatusCode, errbody.Read(resp.Body)))
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<20)).Decode(into); err != nil {
		return fmt.Errorf("anthropic spend limits: decode: %w", err)
	}
	return nil
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
