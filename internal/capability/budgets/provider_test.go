package budgets

import (
	"context"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/governance/budget"
	"go.klarlabs.de/tokenops/internal/contexts/observability/analytics"
)

// billedSource bills per provider; an empty Provider in the filter is all
// of them together.
type billedSource struct {
	byProvider map[string]analytics.Summary
	asked      []analytics.Filter
	daily      []analytics.Filter
}

func (s *billedSource) Summarize(_ context.Context, f analytics.Filter) (analytics.Summary, error) {
	s.asked = append(s.asked, f)
	if f.Provider != "" {
		return s.byProvider[f.Provider], nil
	}
	var all analytics.Summary
	for _, v := range s.byProvider {
		all.CostUSD += v.CostUSD
		all.APIEquivalentUSD += v.APIEquivalentUSD
		all.TotalTokens += v.TotalTokens
	}
	return all, nil
}

func (s *billedSource) AggregateBy(_ context.Context, f analytics.Filter, _ analytics.Bucket, _ analytics.Group) ([]analytics.Row, error) {
	s.daily = append(s.daily, f)
	return nil, nil
}

func (s *billedSource) SummarizeTag(_ context.Context, f analytics.Filter, _ string) (analytics.Summary, error) {
	s.asked = append(s.asked, f)
	return analytics.Summary{CostUSD: 3}, nil
}

func newBilled() *billedSource {
	return &billedSource{byProvider: map[string]analytics.Summary{
		"fireworks": {CostUSD: 40, APIEquivalentUSD: 45, TotalTokens: 4000},
		"anthropic": {CostUSD: 500, APIEquivalentUSD: 900, TotalTokens: 90000},
	}}
}

// A provider budget counts only what that provider billed, on each basis.
func TestAProviderBudgetMeasuresOnlyThatProvidersBill(t *testing.T) {
	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		basis string
		want  float64
	}{
		{"", 40}, {budget.BasisSpend, 40}, {budget.BasisEquivalent, 45}, {budget.BasisTokens, 4000},
	} {
		l := budget.Limit{Name: "mine", Window: budget.WindowMonthly, LimitUSD: 50, LimitTokens: 5000,
			Basis: tc.basis, Provider: "fireworks"}
		v, err := Measure(context.Background(), newBilled(), l, now)
		if err != nil || v != tc.want {
			t.Errorf("basis %q: Measure = %v, %v; want %v", tc.basis, v, err, tc.want)
		}
	}
	all := budget.Limit{Name: "all", Window: budget.WindowMonthly, LimitUSD: 50}
	if v, _ := Measure(context.Background(), newBilled(), all, now); v != 540 {
		t.Errorf("a budget with no provider measured %v, want every provider's 540", v)
	}
}

// The provider narrows the other scopes rather than replacing them: a
// tagged provider budget asks for the tag's work billed by that provider.
func TestAProviderNarrowsTheOtherScopes(t *testing.T) {
	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	src := newBilled()
	l := budget.Limit{Name: "mine", Window: budget.WindowMonthly, LimitUSD: 50, Provider: "fireworks",
		Tag: "client=acme-test", WorkflowID: "wf", AgentID: "ag"}
	if _, err := Measure(context.Background(), src, l, now); err != nil {
		t.Fatal(err)
	}
	f := src.asked[0]
	if f.Provider != "fireworks" || f.WorkflowID != "wf" || f.AgentID != "ag" {
		t.Errorf("asked %+v; want provider, workflow and agent together", f)
	}
}

// Each window of a provider budget is read with the provider, and the
// reading says whose bill it is, for the glance and the menu bar.
func TestReadingsOfAProviderBudgetNameTheProvider(t *testing.T) {
	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	limits := []budget.Limit{
		{Name: "mine", ID: "mine:daily", Window: budget.WindowDaily, LimitUSD: 80, Provider: "fireworks"},
		{Name: "mine", ID: "mine:monthly", Window: budget.WindowMonthly, LimitUSD: 50, Provider: "fireworks", Mode: budget.ModeHard},
	}
	rs := Read(context.Background(), newBilled(), limits, now)
	if len(rs) != 2 {
		t.Fatalf("readings = %+v", rs)
	}
	for _, r := range rs {
		if r.Provider != "fireworks" || r.Used != 40 {
			t.Errorf("reading %s = %+v; want fireworks' 40", r.Key, r)
		}
	}
	if pct := UsedPctOf(rs); pct["mine:monthly"] != 80 || pct["mine"] != 80 {
		t.Errorf("used pct = %v; the hard window's figure is the provider's share", pct)
	}
}

// A provider budget is forecast from that provider's daily history only.
func TestAProviderBudgetIsForecastFromItsOwnHistory(t *testing.T) {
	src := newBilled()
	l := budget.Limit{Name: "mine", Window: budget.WindowMonthly, LimitUSD: 50, Provider: "fireworks"}
	w := &Watcher{Source: src, Limits: []budget.Limit{l}}
	w.Tick(context.Background(), time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC))
	if len(src.daily) != 1 || src.daily[0].Provider != "fireworks" {
		t.Errorf("forecast history asked %+v; want fireworks only", src.daily)
	}
}

// fireworksBill is a provider reporting the operator's own spend this
// month: a bill of $130 against the $40 the price list could cost.
func fireworksBill(_ context.Context, provider string, _ time.Time) (float64, bool) {
	if provider == "fireworks" {
		return 130, true
	}
	return 0, false
}

// A monthly money budget on one provider counts that provider's own bill
// when it reports one: the price list cannot cost every model it routes
// to, so the estimate can read a fraction of a limit already spent.
func TestAMonthlyProviderBudgetCountsTheProvidersBill(t *testing.T) {
	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	limits := []budget.Limit{
		{Name: "fw", ID: "fw:monthly", Window: budget.WindowMonthly, LimitUSD: 100, Provider: "fireworks"},
		{Name: "claude", ID: "claude:monthly", Window: budget.WindowMonthly, LimitUSD: 1000, Provider: "anthropic"},
	}
	rs := ReadBilled(context.Background(), newBilled(), fireworksBill, limits, now)
	if len(rs) != 2 {
		t.Fatalf("readings = %+v", rs)
	}
	if r := rs[0]; r.Used != 130 || r.UsedPct != 130 || r.Source != SourceVendor {
		t.Errorf("fireworks reading = %+v; want the bill's 130, sourced from the vendor", r)
	}
	if r := rs[1]; r.Used != 500 || r.Source != "" {
		t.Errorf("anthropic reading = %+v; a provider without a bill keeps the estimate", r)
	}
}

// Only a budget that counts what the bill counts takes it: money spent
// with the provider this calendar month, unnarrowed.
func TestANarrowedBudgetKeepsTheEstimate(t *testing.T) {
	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	base := budget.Limit{Name: "fw", Window: budget.WindowMonthly, LimitUSD: 100, LimitTokens: 5000, Provider: "fireworks"}
	for name, mut := range map[string]func(*budget.Limit){
		"daily":          func(l *budget.Limit) { l.Window = budget.WindowDaily },
		"weekly":         func(l *budget.Limit) { l.Window = budget.WindowWeekly },
		"tokens":         func(l *budget.Limit) { l.Basis = budget.BasisTokens },
		"equivalent":     func(l *budget.Limit) { l.Basis = budget.BasisEquivalent },
		"tag":            func(l *budget.Limit) { l.Tag = "client=acme-test" },
		"workflow":       func(l *budget.Limit) { l.WorkflowID = "wf" },
		"agent":          func(l *budget.Limit) { l.AgentID = "ag" },
		"every provider": func(l *budget.Limit) { l.Provider = "" },
	} {
		l := base
		mut(&l)
		rs := ReadBilled(context.Background(), newBilled(), fireworksBill, []budget.Limit{l}, now)
		if len(rs) != 1 || rs[0].Source == SourceVendor || rs[0].Used == 130 {
			t.Errorf("%s: readings = %+v; want the estimate", name, rs)
		}
	}
}

// The watcher alerts on the bill: a budget the provider says is spent is
// exceeded, and it is not forecast from the estimate's history.
func TestTheWatcherAlertsOnTheBill(t *testing.T) {
	src := newBilled()
	pub := &events{}
	l := budget.Limit{Name: "fw", Window: budget.WindowMonthly, LimitUSD: 100, Provider: "fireworks"}
	w := &Watcher{Source: src, Limits: []budget.Limit{l}, Publish: pub, Bill: fireworksBill}
	r := w.Tick(context.Background(), time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC))
	if len(pub.got) != 1 {
		t.Errorf("published %d budget.exceeded events; want 1 for the bill's 130 of 100", len(pub.got))
	}
	if len(r.Alerts) == 0 {
		t.Errorf("no alert for a budget the bill has spent")
	}
	if len(src.daily) != 0 {
		t.Errorf("forecast asked %+v; a billed budget is not forecast from the estimate", src.daily)
	}
}
