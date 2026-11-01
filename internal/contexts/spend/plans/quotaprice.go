package plans

import (
	"fmt"
	"strconv"
	"time"
)

// The price of quota (ADR 0017) is a shadow price on a plan window: what
// one more unit of plan use costs now, in the same units as the quality it
// buys. It is 0 while the window lasts to its reset at the pace so far and
// rises towards MaxQuotaPrice as the rest of the window would overrun what
// is left. Routing weighs a model's extra plan use by it, so a dearer tier
// is chosen only when the quality it adds is worth the headroom it takes.
const (
	// MaxQuotaPrice bounds the price: a used-up window. At it, a tier
	// whose quality gain is smaller than the share of plan use it adds
	// is never worth taking.
	MaxQuotaPrice = 1.0
	// quotaPriceShareLeftZeroPct is where a window priced by its share
	// left alone (pace not yet trusted, or no reset known) starts to
	// cost anything: from 0 at a third left to MaxQuotaPrice at none.
	quotaPriceShareLeftZeroPct = 100.0 / 3
)

// How a window's price was worked out.
const (
	// PriceBasisPace is the projected use at the pace so far against
	// what is left.
	PriceBasisPace = "pace"
	// PriceBasisShareLeft is the share left alone: too early in the
	// window to trust its pace, or a reading with no reset.
	PriceBasisShareLeft = "share_left"
	// PriceBasisUsedUp is a window with nothing left.
	PriceBasisUsedUp = "used_up"
)

// QuotaPrice is the price of quota on a plan window, with the figures it
// comes from.
type QuotaPrice struct {
	// Vendor names the plan as its users do ("Claude").
	Vendor string `json:"vendor"`
	// Window is "5h" or "weekly".
	Window string `json:"window"`
	// Lambda is the price, 0 to MaxQuotaPrice.
	Lambda  float64 `json:"lambda"`
	LeftPct float64 `json:"left_pct"`
	// WantedPct is the share of the plan the rest of the window would
	// use at the pace so far; 0 when the pace was not used (Basis).
	WantedPct float64 `json:"wanted_pct,omitempty"`
	// RunsOutBeforeReset is how long before its reset the window runs
	// out at that pace; zero when it lasts or the pace was not used.
	RunsOutBeforeReset time.Duration `json:"runs_out_before_reset_ns,omitempty"`
	// Basis is PriceBasisPace, PriceBasisShareLeft or PriceBasisUsedUp.
	Basis      string    `json:"basis"`
	ResetsAt   time.Time `json:"resets_at,omitzero"`
	ObservedAt time.Time `json:"observed_at,omitzero"`
	FreshUntil time.Time `json:"fresh_until,omitzero"`
	// Source is where the reading came from (claude_code_statusline,
	// daemon_glance, or the vendor window's own source).
	Source string `json:"source,omitempty"`
}

// Price is the price of quota on w at now.
//
// With the pace trusted (as Pressed trusts it: a tenth of the window gone),
// the rest of the window wants wanted = used × remaining ÷ elapsed points
// at the rate so far, and
//
//	λ = max(0, 1 − left ÷ wanted)
//
// which is 0 while the window lasts to its reset, the share of the
// remaining time the window would stand empty otherwise, and below 1 until
// nothing is left. Without a trusted pace, λ = max(0, 1 − left ÷ 33⅓).
func (w VendorWindow) Price(now time.Time) QuotaPrice {
	left := max(0, 100-w.UsedPct)
	p := QuotaPrice{LeftPct: left, ResetsAt: w.ResetsAt, ObservedAt: w.ObservedAt, Source: w.Source}
	if left <= 0 {
		p.Lambda, p.Basis = MaxQuotaPrice, PriceBasisUsedUp
		return p
	}
	if w.Duration > 0 && !w.ResetsAt.IsZero() {
		remaining := w.ResetsAt.Sub(now)
		elapsed := w.Duration - remaining
		if remaining > 0 && remaining <= w.Duration && float64(elapsed) >= pressureMinElapsed*float64(w.Duration) {
			p.Basis = PriceBasisPace
			p.WantedPct = w.UsedPct * float64(remaining) / float64(elapsed)
			if p.WantedPct > left {
				p.Lambda = min(MaxQuotaPrice, 1-left/p.WantedPct)
				// At the rate so far, what is left lasts left ÷ used of
				// the time already gone.
				p.RunsOutBeforeReset = remaining - time.Duration(left/w.UsedPct*float64(elapsed))
			}
			return p
		}
	}
	p.Basis = PriceBasisShareLeft
	p.Lambda = max(0, 1-left/quotaPriceShareLeftZeroPct)
	return p
}

// BindingPrice is the price of the plan: that of its binding window, the
// one with the highest price (the least left on a tie), among its 5h and
// weekly windows. A plan is held to every window at once, so the tightest
// sets what one more unit of use costs. Nil when no window can be priced:
// model-scoped and unnamed windows bind one model, not the plan, and stale
// or already-reset windows say nothing about now.
func BindingPrice(vendor string, windows []VendorWindow, now time.Time) *QuotaPrice {
	var best *QuotaPrice
	for _, w := range windows {
		label, ok := pressureLabels[w.Name]
		if !ok || w.Stale || (!w.ResetsAt.IsZero() && !w.ResetsAt.After(now)) {
			continue
		}
		p := w.Price(now)
		p.Vendor, p.Window = vendor, label
		if best == nil || p.Lambda > best.Lambda || (p.Lambda == best.Lambda && p.LeftPct < best.LeftPct) {
			best = &p
		}
	}
	return best
}

// Why says, in the operator's words, what the price is and why.
func (p *QuotaPrice) Why() string {
	if p == nil {
		return ""
	}
	price := "quota price " + FormatQuotaPrice(p.Lambda)
	switch {
	case p.Basis == PriceBasisUsedUp:
		return fmt.Sprintf("%s %s is used up (%s)", p.Vendor, p.Window, price)
	case p.Basis == PriceBasisPace && p.RunsOutBeforeReset > 0:
		return fmt.Sprintf("%s %s runs out ~%s before it resets at this pace (%s left, ~%s wanted; %s)",
			p.Vendor, p.Window, pressureSpan(p.RunsOutBeforeReset), pressureLeft(p.LeftPct), pressureLeft(p.WantedPct), price)
	case p.Basis == PriceBasisPace:
		return fmt.Sprintf("%s %s lasts to its reset at this pace (%s left, ~%s wanted; %s)",
			p.Vendor, p.Window, pressureLeft(p.LeftPct), pressureLeft(p.WantedPct), price)
	}
	return fmt.Sprintf("%s %s has %s left (%s)", p.Vendor, p.Window, pressureLeft(p.LeftPct), price)
}

// Evidence is the price as a ledger records it: the figures, where they
// came from and until when they hold.
func (p *QuotaPrice) Evidence() string {
	if p == nil {
		return ""
	}
	s := fmt.Sprintf("quota_price: λ %s from %s %s, basis %s; %s", FormatQuotaPrice(p.Lambda), p.Vendor, p.Window, p.Basis, p.Why())
	if p.Source != "" {
		s += "; source " + p.Source
	}
	if !p.ObservedAt.IsZero() {
		s += "; observed " + p.ObservedAt.UTC().Format(time.RFC3339)
	}
	if !p.FreshUntil.IsZero() {
		s += "; fresh until " + p.FreshUntil.UTC().Format(time.RFC3339)
	}
	return s
}

// FormatQuotaPrice renders a price to two places, and 0 as "0".
func FormatQuotaPrice(v float64) string {
	if v <= 0 {
		return "0"
	}
	return strconv.FormatFloat(v, 'f', 2, 64)
}
