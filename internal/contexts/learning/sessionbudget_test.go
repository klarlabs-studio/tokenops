package learning

import (
	"strings"
	"testing"
	"time"
)

var budgetNow = time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)

// costs builds one session per figure, all seen a day ago.
func costs(usd ...float64) []SessionCost {
	out := make([]SessionCost, len(usd))
	for i, v := range usd {
		out[i] = SessionCost{Session: "s" + string(rune('a'+i%26)) + string(rune('a'+i/26)), USD: v, LastSeen: budgetNow.Add(-24 * time.Hour)}
	}
	return out
}

func spread(n int, lo, step float64) []float64 {
	out := make([]float64, n)
	for i := range out {
		out[i] = lo + float64(i)*step
	}
	return out
}

func TestDeriveSessionBudget(t *testing.T) {
	p := DefaultBudgetPolicy(50)
	tests := []struct {
		name       string
		in         []SessionCost
		prev       *SessionBudgetBelief
		wantSource string
		wantUSD    float64
		wantClamp  string
		wantN      int
	}{
		{
			name:       "too few sessions keeps the fixed default",
			in:         costs(spread(p.MinSessions-1, 10, 10)...),
			wantSource: SourceDefault, wantUSD: 50, wantN: p.MinSessions - 1,
		},
		{
			// 1..100 dollars: the nearest-rank p90 is 90.
			name:       "p90 of the user's own sessions",
			in:         costs(spread(100, 1, 1)...),
			wantSource: SourceHistory, wantUSD: 90, wantN: 100,
		},
		{
			name:       "a tiny p90 is lifted to the floor",
			in:         costs(spread(30, 0.1, 0.1)...),
			wantSource: SourceHistory, wantUSD: p.FloorUSD, wantClamp: ClampFloor, wantN: 30,
		},
		{
			name:       "a huge p90 is held at the ceiling",
			in:         costs(spread(30, 1000, 100)...),
			wantSource: SourceHistory, wantUSD: p.CeilingUSD, wantClamp: ClampCeiling, wantN: 30,
		},
		{
			// p90 is 90, but last week's belief was 60: it may only move 25%.
			name:       "upward drift is clamped against the previous belief",
			in:         costs(spread(100, 1, 1)...),
			prev:       &SessionBudgetBelief{BudgetUSD: 60, Source: SourceHistory},
			wantSource: SourceHistory, wantUSD: 75, wantClamp: ClampDriftUp, wantN: 100,
		},
		{
			name:       "downward drift is clamped too",
			in:         costs(spread(100, 1, 1)...),
			prev:       &SessionBudgetBelief{BudgetUSD: 200, Source: SourceHistory},
			wantSource: SourceHistory, wantUSD: 150, wantClamp: ClampDriftDown, wantN: 100,
		},
		{
			name:       "a previous default is no anchor for drift",
			in:         costs(spread(100, 1, 1)...),
			prev:       &SessionBudgetBelief{BudgetUSD: 50, Source: SourceDefault},
			wantSource: SourceHistory, wantUSD: 90, wantN: 100,
		},
		{
			name: "sessions outside the window and unpriced ones do not count",
			in: append(costs(spread(25, 1, 1)...),
				SessionCost{Session: "old", USD: 9999, LastSeen: budgetNow.Add(-31 * 24 * time.Hour)},
				SessionCost{Session: "free", USD: 0, LastSeen: budgetNow}),
			// 1..25: nearest-rank p90 is ceil(22.5)=23.
			wantSource: SourceHistory, wantUSD: 23, wantN: 25,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			b := DeriveSessionBudget(tc.in, tc.prev, p, budgetNow)
			if b.Source != tc.wantSource || b.BudgetUSD != tc.wantUSD || b.Clamp != tc.wantClamp || b.Sessions != tc.wantN {
				t.Fatalf("got source=%s usd=%v clamp=%q n=%d, want %s %v %q %d",
					b.Source, b.BudgetUSD, b.Clamp, b.Sessions, tc.wantSource, tc.wantUSD, tc.wantClamp, tc.wantN)
			}
			if !b.DerivedAt.Equal(budgetNow) {
				t.Errorf("DerivedAt = %v", b.DerivedAt)
			}
		})
	}
}

func TestSessionBudgetProvenance(t *testing.T) {
	p := DefaultBudgetPolicy(50)
	b := DeriveSessionBudget(costs(spread(113, 1, 1)...), nil, p, budgetNow)
	if got := b.Provenance(); got != "your p90 session, from 113 sessions" {
		t.Errorf("Provenance = %q", got)
	}
	d := DeriveSessionBudget(costs(1, 2, 3), nil, p, budgetNow)
	if got := d.Provenance(); !strings.Contains(got, "default") || !strings.Contains(got, "3 of 20") {
		t.Errorf("default provenance should say why: %q", got)
	}
}

func TestSessionBudgetStale(t *testing.T) {
	p := DefaultBudgetPolicy(50)
	b := SessionBudgetBelief{DerivedAt: budgetNow.Add(-6 * 24 * time.Hour)}
	if b.Stale(budgetNow, p) {
		t.Error("six days old is fresh under a weekly refresh")
	}
	b.DerivedAt = budgetNow.Add(-8 * 24 * time.Hour)
	if !b.Stale(budgetNow, p) {
		t.Error("eight days old is stale")
	}
	if !(SessionBudgetBelief{}).Stale(budgetNow, p) {
		t.Error("a belief never derived is stale")
	}
	b.DerivedAt = budgetNow.Add(time.Hour)
	if !b.Stale(budgetNow, p) {
		t.Error("a belief from the future (clock moved back) is stale")
	}
}

// Tips the operator calls "expected" say the bar sits too low for them:
// enough of them move it from p90 to p95.
func TestDeriveSessionBudget_ExpectedAnswersRaiseThePercentile(t *testing.T) {
	p := DefaultBudgetPolicy(50)
	in := costs(spread(100, 1, 1)...)
	tests := []struct {
		name    string
		answers AlertAnswers
		want    float64
	}{
		{"no answers keep p90", AlertAnswers{}, 90},
		{"too few answers keep p90", AlertAnswers{Expected: 2, Answered: 2}, 90},
		{"mostly not expected keeps p90", AlertAnswers{Expected: 1, Answered: 4}, 90},
		{"mostly expected moves to p95", AlertAnswers{Expected: 3, Answered: 4}, 95},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			b := DeriveSessionBudgetAnswered(in, tc.answers, nil, p, budgetNow)
			if b.BudgetUSD != tc.want {
				t.Fatalf("budget = %v, want %v", b.BudgetUSD, tc.want)
			}
			if b.ExpectedAnswers != tc.answers.Expected || b.Answered != tc.answers.Answered {
				t.Errorf("answers not kept as evidence: %+v", b)
			}
		})
	}
	b := DeriveSessionBudgetAnswered(in, AlertAnswers{Expected: 3, Answered: 4}, nil, p, budgetNow)
	if got := b.Provenance(); got != "your p95 session, from 100 sessions" {
		t.Errorf("Provenance = %q", got)
	}
}
