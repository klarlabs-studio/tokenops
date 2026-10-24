package boundaryledger

import "testing"

func TestBudgetLeftWordsReadMoneyFirstAndCountDown(t *testing.T) {
	cases := []struct {
		name string
		r    BudgetReading
		want string
	}{
		{"spend", BudgetReading{Basis: "spend", Used: 8.2, Limit: 10, UsedPct: 82}, "$1.80 of $10.00 left, 82% spent"},
		{"tokens", BudgetReading{Basis: "tokens", Used: 300_000, Limit: 1_000_000, UsedPct: 30}, "700k tokens of 1.0M tokens left, 30% spent"},
		{"over", BudgetReading{Basis: "spend", Used: 10.4, Limit: 10, UsedPct: 104}, "used up, $10.40 of $10.00 spent"},
		{"no limit", BudgetReading{UsedPct: 30}, "70% left, 30% spent"},
		{"no limit, over", BudgetReading{UsedPct: 100}, "used up"},
	}
	for _, c := range cases {
		if got := c.r.LeftWords(); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}
