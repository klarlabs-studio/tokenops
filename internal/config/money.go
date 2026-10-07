package config

import (
	"fmt"
	"strings"
)

// MoneyConfig is the operator's currency. Usage is priced in US dollars
// from vendor rate cards; plans are paid in whatever the bill says, and
// every total is shown in this currency with the rate it used.
type MoneyConfig struct {
	// Currency is an ISO 4217 code, e.g. EUR. Empty means USD.
	Currency string `yaml:"currency,omitempty"`
	// PerUSD is how many units of Currency one US dollar buys, e.g. 0.85.
	// Set it to pin a rate; leave it empty to use the ECB's daily
	// reference rate.
	PerUSD float64 `yaml:"per_usd,omitempty"`
	// FetchRate fetches the ECB's daily reference rate, at most once a
	// day, when PerUSD is not set. It sends nothing. Default on; false
	// keeps TokenOps off the network for this, and amounts not in USD
	// then need PerUSD.
	FetchRate *bool `yaml:"fetch_rate,omitempty"`
}

// FetchesRate reports whether the ECB rate may be fetched.
func (m MoneyConfig) FetchesRate() bool { return m.FetchRate == nil || *m.FetchRate }

// Validate rejects a currency code or rate that cannot mean anything.
func (m MoneyConfig) Validate() error {
	if c := strings.TrimSpace(m.Currency); c != "" && len(c) != 3 {
		return fmt.Errorf("money.currency must be a three-letter ISO 4217 code, got %q", m.Currency)
	}
	if m.PerUSD < 0 {
		return fmt.Errorf("money.per_usd must not be negative, got %g", m.PerUSD)
	}
	return nil
}
