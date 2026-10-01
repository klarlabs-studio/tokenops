// Package money shows US-dollar amounts in the operator's currency, with
// the rate they were converted at, so a surface never sets euros beside
// dollars as if they were one unit.
package money

import (
	"fmt"

	"go.klarlabs.de/tokenops/internal/contexts/spend/fx"
)

// Rate converts US dollars into the operator's currency.
type Rate = fx.Rate

// Display is a window's money in the operator's currency.
type Display struct {
	Currency      string  `json:"currency"`
	Rate          Rate    `json:"rate"`
	Cost          float64 `json:"cost"`
	APIEquivalent float64 `json:"api_equivalent"`
}

// Converts reports whether amounts are shown in a currency other than
// USD.
func Converts(r Rate) bool { return r.Valid() && !r.IsUSD() }

// Show converts a window's billed cost and list-price value, or returns
// nil when there is nothing to convert.
func Show(r Rate, costUSD, apiEquivalentUSD float64) *Display {
	if !Converts(r) {
		return nil
	}
	return &Display{
		Currency: r.Currency, Rate: r,
		Cost: r.FromUSD(costUSD), APIEquivalent: r.FromUSD(apiEquivalentUSD),
	}
}

// Format renders a US-dollar amount in the operator's currency, or
// returns ok=false when it stays in dollars.
func Format(r Rate, usd float64) (string, bool) {
	if !Converts(r) {
		return "", false
	}
	return fmt.Sprintf("%.2f %s", r.FromUSD(usd), r.Currency), true
}
