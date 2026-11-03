package orgcaps

import (
	"context"
	"errors"
	"strings"
	"testing"

	copilotapi "go.klarlabs.de/tokenops/internal/infra/vendorusage/copilot"
	"go.klarlabs.de/tokenops/pkg/teamwire"
)

type fakeBudgets struct {
	budgets []copilotapi.Budget
	err     error
}

func (f fakeBudgets) OrgBudgets(context.Context, string) ([]copilotapi.Budget, error) {
	return f.budgets, f.err
}

func dollars(v float64) *float64 { return &v }

func TestReadCopilotKeepsCopilotBudgets(t *testing.T) {
	r := fakeBudgets{budgets: []copilotapi.Budget{
		{Scope: "organization", Entity: "acme", SKU: "actions", Amount: 50, Stops: true},
		{Scope: "organization", Entity: "acme", SKU: "premium_requests", Amount: 200, Consumed: dollars(80)},
		{Scope: "user", User: "AnnB", SKU: "ai_credits", Amount: 30, Consumed: dollars(28.5), Stops: true},
		{Scope: "user", User: "zed", SKU: "copilot_premium_request", Amount: 0, Stops: true},
	}}
	rep, err := ReadCopilot(context.Background(), r, "acme", []teamwire.Member{{ID: "m1", Name: "annb"}})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Vendor != "Copilot" || len(rep.Rows) != 3 {
		t.Fatalf("report = %+v", rep)
	}
	ann, org, zed := rep.Rows[0], rep.Rows[1], rep.Rows[2]
	if ann.MemberID != "m1" || ann.Member != "annb" || *ann.UsedPct != 95 || !ann.Near() || ann.Shared ||
		ann.Source != "user budget on ai_credits" {
		t.Errorf("ann = %+v", ann)
	}
	if org.Member != "acme" || !org.Shared || *org.UsedPct != 40 || org.Source != "organization budget on premium_requests (alerts only)" {
		t.Errorf("org = %+v", org)
	}
	if zed.MemberID != "" || zed.Shared || *zed.Limit != 0 || zed.UsedPct != nil {
		t.Errorf("zed = %+v", zed)
	}
}

func TestReadCopilotNamesTheAccessNeeded(t *testing.T) {
	_, err := ReadCopilot(context.Background(), fakeBudgets{err: copilotapi.ErrBudgetsAccess}, "acme", nil)
	if !errors.Is(err, copilotapi.ErrBudgetsAccess) || !strings.Contains(err.Error(), "billing manager of acme") {
		t.Fatalf("err = %v", err)
	}
}
