package scorecard

import (
	"fmt"
	"strings"
)

// UnitCost is what one unit of finished work cost over the scorecard's
// period, against the four weeks before it: a merged pull request, an
// achieved outcome. Reported, not graded — a dearer pull request may be a
// harder one.
type UnitCost struct {
	Unit  string `json:"unit"`
	Units int    `json:"units"`
	// PerUnitUSD is at API-equivalent prices.
	PerUnitUSD         float64  `json:"per_unit_usd"`
	BaselinePerUnitUSD *float64 `json:"baseline_per_unit_usd,omitempty"`
	ChangePct          *float64 `json:"change_pct,omitempty"`
	// Direction reads ChangePct: cheaper finished work is better.
	Direction Direction `json:"direction,omitempty"`
}

// writeUnitCosts renders the unit costs with any in the period.
func writeUnitCosts(b *strings.Builder, us []UnitCost, days int) {
	var rows []UnitCost
	for _, u := range us {
		if u.Units > 0 {
			rows = append(rows, u)
		}
	}
	if len(rows) == 0 {
		return
	}
	if days <= 0 {
		days = 7
	}
	fmt.Fprintf(b, "\nUnit costs (last %d days, against the 4 weeks before; API prices):\n", days)
	for _, u := range rows {
		noun := "units"
		if u.Units == 1 {
			noun = "unit"
		}
		line := fmt.Sprintf("  %-18s $%.2f each · %d %s", u.Unit, u.PerUnitUSD, u.Units, noun)
		if u.BaselinePerUnitUSD != nil {
			line += fmt.Sprintf(" · baseline $%.2f", *u.BaselinePerUnitUSD)
		}
		if u.ChangePct != nil {
			if u.Direction != "" {
				line += fmt.Sprintf(" (%+.0f%%, %s)", *u.ChangePct, u.Direction)
			} else {
				line += fmt.Sprintf(" (%+.0f%%)", *u.ChangePct)
			}
		}
		fmt.Fprintln(b, line)
	}
}
