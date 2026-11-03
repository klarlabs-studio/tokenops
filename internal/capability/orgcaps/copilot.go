package orgcaps

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	copilotapi "go.klarlabs.de/tokenops/internal/infra/vendorusage/copilot"
	"go.klarlabs.de/tokenops/pkg/teamwire"
)

// BudgetReader reads a GitHub organisation's billing budgets.
type BudgetReader interface {
	OrgBudgets(ctx context.Context, org string) ([]copilotapi.Budget, error)
}

// ReadCopilot reads the Copilot budgets of a GitHub organisation: those on
// the Copilot product or its SKUs, premium requests, or AI credits. A user
// budget names a GitHub login, which is matched to a team member of that
// name; GitHub has no address to match by.
func ReadCopilot(ctx context.Context, r BudgetReader, org string, members []teamwire.Member) (Report, error) {
	budgets, err := r.OrgBudgets(ctx, org)
	if err != nil {
		if errors.Is(err, copilotapi.ErrBudgetsAccess) {
			return Report{}, fmt.Errorf("%w: sign in as an owner or billing manager of %s (`gh auth refresh -s admin:org` for the GitHub CLI's token)", err, org)
		}
		return Report{}, err
	}
	byName := map[string]teamwire.Member{}
	for _, m := range members {
		byName[strings.ToLower(m.Name)] = m
	}
	rep := Report{Vendor: "Copilot"}
	for _, b := range budgets {
		if !copilotapi.IsCopilotSKU(b.SKU) {
			continue
		}
		limit := b.Amount
		row := Row{Member: budgetHolder(b), Vendor: rep.Vendor, Period: "monthly", Currency: "USD",
			Limit: &limit, Source: budgetSource(b)}
		row.Shared = b.User == ""
		if b.User != "" {
			if m, ok := byName[strings.ToLower(b.User)]; ok {
				row.Member, row.MemberID = m.Name, m.ID
			}
		}
		if b.Consumed != nil {
			row.Spent = *b.Consumed
			if limit > 0 {
				pct := 100 * row.Spent / limit
				row.UsedPct = &pct
			}
		}
		rep.Rows = append(rep.Rows, row)
	}
	slices.SortStableFunc(rep.Rows, byUrgency)
	return rep, nil
}

// budgetHolder is who or what a budget caps: the user's login, or the
// organisation, repository or cost center by name.
func budgetHolder(b copilotapi.Budget) string {
	if b.User != "" {
		return b.User
	}
	if b.Entity != "" {
		return b.Entity
	}
	return b.Scope
}

// budgetSource says what a budget counts and whether it stops usage:
// "organization budget on premium_requests", "… (alerts only)".
func budgetSource(b copilotapi.Budget) string {
	scope := strings.ReplaceAll(cmp.Or(b.Scope, "organization"), "_", " ")
	out := fmt.Sprintf("%s budget on %s", scope, b.SKU)
	if !b.Stops {
		out += " (alerts only)"
	}
	return out
}
