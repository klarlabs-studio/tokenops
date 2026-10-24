package boundaryledger

import (
	"fmt"
	"math"
)

// LeftWords words a budget window by what is left, money first, the way a
// fuel gauge reads: "$1.80 of $10.00 left, 82% spent", or "used up,
// $10.40 of $10.00 spent" once nothing is. A reading without a limit
// words the shares alone: "18% left, 82% spent".
func (r BudgetReading) LeftWords() string {
	spent := int(math.Floor(r.UsedPct))
	if r.UsedPct >= 100 {
		if r.Limit > 0 {
			return fmt.Sprintf("used up, %s of %s spent", r.amount(r.Used), r.amount(r.Limit))
		}
		return "used up"
	}
	if r.Limit > 0 {
		return fmt.Sprintf("%s of %s left, %d%% spent", r.amount(math.Max(0, r.Limit-r.Used)), r.amount(r.Limit), spent)
	}
	return fmt.Sprintf("%d%% left, %d%% spent", 100-spent, spent)
}

// amount words v in the window's unit: dollars, or tokens.
func (r BudgetReading) amount(v float64) string {
	if r.Basis != "tokens" {
		return fmt.Sprintf("$%.2f", v)
	}
	n := int64(v)
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM tokens", float64(n)/1_000_000)
	case n >= 1_000:
		return fmt.Sprintf("%dk tokens", n/1_000)
	}
	return fmt.Sprintf("%d tokens", n)
}
