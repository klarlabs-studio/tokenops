package allocation

import (
	"context"
	"errors"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/budgets"
	"go.klarlabs.de/tokenops/internal/contexts/governance/budget"
	"go.klarlabs.de/tokenops/internal/contexts/observability/analytics"
	"go.klarlabs.de/tokenops/internal/infra/sessiondirs"
)

type turnReader struct {
	turns []analytics.SessionTurn
	asked analytics.Filter
}

func (r *turnReader) SessionTurns(_ context.Context, f analytics.Filter) ([]analytics.SessionTurn, error) {
	r.asked = f
	return r.turns, nil
}

type allSpend struct{}

func (allSpend) Summarize(context.Context, analytics.Filter) (analytics.Summary, error) {
	return analytics.Summary{CostUSD: 1000}, nil
}

func (allSpend) AggregateBy(context.Context, analytics.Filter, analytics.Bucket, analytics.Group) ([]analytics.Row, error) {
	return nil, nil
}

func TestABudgetScopedToATagMeasuresOnlyItsWork(t *testing.T) {
	covered := turn("a", "claude-code-jsonl", 0, 4)
	covered.PlanCovered, covered.InputTokens, covered.OutputTokens = true, 900, 100
	metered := turn("a", "claude-code-jsonl", time.Minute, 3)
	other := turn("b", "claude-code-jsonl", 0, 50)
	tr := &turnReader{turns: []analytics.SessionTurn{covered, metered, other}}
	meter := &TagMeter{Turns: tr, Rules: rules, Dirs: func(time.Time) map[string]sessiondirs.Dir {
		return map[string]sessiondirs.Dir{"a": {CWD: "/w/clients/acme"}, "b": {CWD: "/w/else"}}
	}}
	src := BudgetSource(allSpend{}, meter)
	l := budget.Limit{Name: "acme", Window: budget.WindowMonthly, Basis: budget.BasisEquivalent, LimitUSD: 10, Tag: "client=acme", AgentID: "ag"}
	now := t0.Add(2 * time.Hour)
	v, err := budgets.Measure(context.Background(), src, l, now)
	if err != nil || v != 7 {
		t.Fatalf("equivalent = %v, %v; want 7", v, err)
	}
	if tr.asked.AgentID != "ag" || !tr.asked.Until.Equal(now) {
		t.Errorf("turns read with %+v", tr.asked)
	}
	l.Basis = budget.BasisSpend
	if v, _ := budgets.Measure(context.Background(), src, l, now); v != 3 {
		t.Errorf("billed = %v, want 3: plan-covered work bills nothing", v)
	}
	l.Basis, l.LimitTokens = budget.BasisTokens, 10_000
	if v, _ := budgets.Measure(context.Background(), src, l, now); v != 2000 {
		t.Errorf("tokens = %v, want 2000", v)
	}
	l.Tag = ""
	l.Basis = budget.BasisSpend
	if v, _ := budgets.Measure(context.Background(), src, l, now); v != 1000 {
		t.Errorf("an untagged budget = %v, want all spend", v)
	}
}

func TestATagWithoutRulesIsAnError(t *testing.T) {
	m := &TagMeter{Turns: &turnReader{}}
	if _, err := m.SummarizeTag(context.Background(), analytics.Filter{}, "client=acme"); !errors.Is(err, ErrNoRules) {
		t.Errorf("err = %v, want ErrNoRules", err)
	}
}
