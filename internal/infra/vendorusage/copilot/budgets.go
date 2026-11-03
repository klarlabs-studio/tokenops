package copilot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"go.klarlabs.de/tokenops/internal/infra/vendorusage/errbody"
)

// GitHub's billing budgets (docs.github.com, "REST API endpoints for
// budgets", read 2026-11-03) cap an organisation's metered spend per
// product or SKU, for the organisation, a repository, a cost center or a
// user. An organisation admin or billing manager reads them. TokenOps only
// reads them.

// ErrBudgetsAccess is a token that may not read the organisation's budgets.
var ErrBudgetsAccess = errors.New("this GitHub token may not read the organisation's budgets: it needs an organisation admin or billing manager")

// budgetsAPIVersion is the REST API version the budgets endpoints are
// documented under.
const budgetsAPIVersion = "2026-03-10"

// budgetPages bounds the pages followed, at 100 budgets a page.
const budgetPages = 100

// Budget is one of an organisation's billing budgets. Amounts are in US
// dollars.
type Budget struct {
	ID string
	// Scope is organization, repository, cost_center, user, …; Entity
	// names the organisation, repository or cost center, and User the
	// login a user budget is for.
	Scope  string
	Entity string
	User   string
	// SKU is the product or SKU the budget counts (copilot,
	// premium_requests, ai_credits, actions, …).
	SKU    string
	Amount float64
	// Consumed is the spend so far this month; nil when GitHub sent none.
	Consumed *float64
	// Stops is whether usage is refused once the budget is spent.
	Stops bool
}

// BudgetClient reads an organisation's budgets with a GitHub token.
type BudgetClient struct {
	BaseURL    string
	Token      string
	HTTPClient *http.Client
}

// OrgBudgets lists every budget of org.
func (c *BudgetClient) OrgBudgets(ctx context.Context, org string) ([]Budget, error) {
	if c.Token == "" {
		return nil, ErrNoGitHubToken
	}
	base, hc := c.BaseURL, c.HTTPClient
	if base == "" {
		base = "https://api.github.com"
	}
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
	}
	var out []Budget
	for page := 1; ; page++ {
		if page > budgetPages {
			return out, errors.New("github budgets: too many pages")
		}
		q := url.Values{}
		q.Set("per_page", "100")
		q.Set("page", strconv.Itoa(page))
		endpoint := base + "/organizations/" + url.PathEscape(org) + "/settings/billing/budgets?" + q.Encode()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			return out, fmt.Errorf("github budgets: build request: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+c.Token)
		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("X-GitHub-Api-Version", budgetsAPIVersion)
		var body struct {
			Budgets []struct {
				ID       string   `json:"id"`
				Scope    string   `json:"budget_scope"`
				Entity   string   `json:"budget_entity_name"`
				User     string   `json:"user"`
				SKU      string   `json:"budget_product_sku"`
				Amount   float64  `json:"budget_amount"`
				Consumed *float64 `json:"consumed_amount"`
				Stops    bool     `json:"prevent_further_usage"`
			} `json:"budgets"`
			HasNext bool `json:"has_next_page"`
		}
		if err := getBudgets(hc, req, &body); err != nil {
			return out, err
		}
		for _, b := range body.Budgets {
			out = append(out, Budget{ID: b.ID, Scope: b.Scope, Entity: b.Entity, User: b.User, SKU: b.SKU,
				Amount: b.Amount, Consumed: b.Consumed, Stops: b.Stops})
		}
		if !body.HasNext || len(body.Budgets) == 0 {
			return out, nil
		}
	}
}

// ErrNoGitHubToken is returned before any call without a token.
var ErrNoGitHubToken = errors.New("github budgets: no GitHub token")

func getBudgets(hc *http.Client, req *http.Request, into any) error {
	resp, err := hc.Do(req)
	if err != nil {
		return fmt.Errorf("github budgets: do request: %w", err)
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusForbidden, resp.StatusCode == http.StatusNotFound:
		return fmt.Errorf("github budgets: %w (%s)", ErrBudgetsAccess, errbody.Status(resp.StatusCode, errbody.Read(resp.Body)))
	case resp.StatusCode < 200 || resp.StatusCode >= 300:
		return fmt.Errorf("github budgets: %s", errbody.Status(resp.StatusCode, errbody.Read(resp.Body)))
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 16<<20)).Decode(into); err != nil {
		return fmt.Errorf("github budgets: decode: %w", err)
	}
	return nil
}

// IsCopilotSKU reports whether a budget counts Copilot: the Copilot
// product or one of its SKUs, its premium requests, or AI credits.
func IsCopilotSKU(sku string) bool {
	s := strings.ToLower(sku)
	return strings.Contains(s, "copilot") || strings.Contains(s, "premium_request") || s == "ai_credits"
}
