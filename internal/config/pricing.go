package config

import (
	"time"
)

// PricingConfig points at an optional YAML rate file that is layered on
// top of the built-in list-price catalog (spend.TableWithOverrides).
// Use it to price newly released models before a tokenops upgrade, or
// to apply negotiated rates. Same schema as the embedded pricing.yaml.
type PricingConfig struct {
	Path string `yaml:"path"`
	// Refresh keeps the rate card current without anyone remembering to
	// run `tokenops pricing refresh`.
	Refresh PricingRefreshConfig `yaml:"refresh,omitempty"`
}

// PricingRefreshConfig governs the daemon's automatic rate-card refresh.
//
// This is one of two outbound calls tokenops makes on its own (the other
// is the ECB exchange rate, see MoneyConfig). It fetches a
// public rate card (LiteLLM's model_prices_and_context_window.json) and
// sends nothing: no prompt, no file, no identifier, no usage. The privacy
// claim is about content, and no content is involved — but a tool that
// starts talking to the network without saying so has spent trust it
// cannot buy back, which is why this is documented and switchable rather
// than quietly always-on.
type PricingRefreshConfig struct {
	// Disabled turns the automatic refresh off. Off by absence would be
	// the safer default for a network call, but a rate card nobody
	// refreshes is how a session gets priced at $0 on a model released
	// after the binary — which is the failure this exists to prevent.
	Disabled bool `yaml:"disabled,omitempty"`
	// Interval is how often to check. Zero takes DefaultPricingRefresh.
	// Values below the floor are raised to it: rate cards change on the
	// order of weeks, and hammering a public file helps nobody.
	Interval time.Duration `yaml:"interval,omitempty"`
}

// Pricing-refresh defaults. A day is far more often than list prices
// move, and still catches a model released overnight before the next
// session is mispriced.
const (
	DefaultPricingRefresh = 24 * time.Hour
	MinPricingRefresh     = time.Hour
)

// Enabled reports whether the daemon should refresh the rate card.
func (p PricingRefreshConfig) Enabled() bool { return !p.Disabled }

// Every resolves the check interval, applying the default and the floor.
func (p PricingRefreshConfig) Every() time.Duration {
	if p.Interval <= 0 {
		return DefaultPricingRefresh
	}
	if p.Interval < MinPricingRefresh {
		return MinPricingRefresh
	}
	return p.Interval
}
