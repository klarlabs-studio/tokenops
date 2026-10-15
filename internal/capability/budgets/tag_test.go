package budgets

import (
	"context"
	"strings"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/governance/budget"
	"go.klarlabs.de/tokenops/internal/contexts/observability/analytics"
)

// taggedSource spends cost in all, of which perTag carries each tag.
type taggedSource struct {
	spendSource
	perTag map[string]float64
	asked  []analytics.Filter
}

func (s *taggedSource) SummarizeTag(_ context.Context, f analytics.Filter, tag string) (analytics.Summary, error) {
	s.asked = append(s.asked, f)
	return analytics.Summary{CostUSD: s.perTag[tag]}, nil
}

// A budget scoped to a tag measures only the work carrying it, over the
// budget's window and scope.
func TestATaggedBudgetMeasuresTheTagsWork(t *testing.T) {
	src := &taggedSource{spendSource: spendSource{cost: 500}, perTag: map[string]float64{"client=acme": 40}}
	l := budget.Limit{Name: "acme", Window: budget.WindowMonthly, LimitUSD: 50, Basis: budget.BasisSpend,
		Tag: "client=acme", WorkflowID: "wf"}
	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	v, err := Measure(context.Background(), src, l, now)
	if err != nil || v != 40 {
		t.Fatalf("Measure = %v, %v; want 40", v, err)
	}
	if f := src.asked[0]; !f.Since.Equal(budget.WindowStart(budget.WindowMonthly, now)) || f.WorkflowID != "wf" {
		t.Errorf("asked %+v", f)
	}
	w := &Watcher{Source: src, Limits: []budget.Limit{l}}
	r := w.Tick(context.Background(), now)
	if len(r.Alerts) != 1 || r.Alerts[0].ActualUSD != 40 {
		t.Errorf("alerts = %+v", r.Alerts)
	}
}

// A source that cannot tell tags apart does not measure a tagged budget as
// all spend: the budget is skipped, with the reason.
func TestATaggedBudgetWithoutAllocationIsNotMeasuredAsAllSpend(t *testing.T) {
	l := budget.Limit{Name: "acme", Window: budget.WindowMonthly, LimitUSD: 50, Tag: "client=acme"}
	_, err := Measure(context.Background(), &spendSource{cost: 500}, l, time.Now())
	if err == nil || !strings.Contains(err.Error(), "allocation") {
		t.Errorf("Measure error = %v, want one naming allocation", err)
	}
	if got := UsedPct(context.Background(), &spendSource{cost: 500}, []budget.Limit{l}, time.Now()); len(got) != 0 {
		t.Errorf("UsedPct = %v, want the budget left out", got)
	}
}
