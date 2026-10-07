package coachhook

import (
	"fmt"
	"math"

	"go.klarlabs.de/tokenops/internal/contexts/spend/spend"
)

// nudgeMessage builds the operator-facing, escalating nudge for a fired
// budget fraction.
//
// Three things about the wording are deliberate, because the earlier
// version got all three wrong and the result read as a bill that did not
// add up.
//
// It does not call the shipping default "your budget". An operator on
// Claude Max pays $200 a month and never chose a $50 figure; a possessive
// on a number they did not set makes it look like a charge they agreed
// to.
//
// Every tier says API-equivalent, not just the quietest one. Previously
// the louder the warning got, the more it read like real money — exactly
// backwards.
//
// And it says outright that nothing is being charged. On a subscription
// this figure is a counterfactual: what the session would have cost at
// list price, which is the only way to see context drift compounding when
// the actual bill is flat and identical either way.
func nudgeMessage(frac, cumulative, budget float64, configured bool) string {
	pct := int(math.Round(frac * 100))
	budgetStr := formatUSD(budget)
	cumStr := fmt.Sprintf("$%.2f", cumulative)

	// Only a budget the operator set is theirs.
	ceiling := "the default " + budgetStr + " session ceiling"
	if configured {
		ceiling = "your " + budgetStr + " session budget"
	}

	switch {
	case frac > 1.0+fracEpsilon:
		return fmt.Sprintf("tokenops: %s API-equivalent this session — %d%% of %s, "+
			"and not a charge. Long sessions compound cache-read fast; /compact or split the task.",
			cumStr, pct, ceiling)
	case frac >= 1.0-fracEpsilon:
		return fmt.Sprintf("tokenops: %s API-equivalent this session, past %s "+
			"(not a charge — your plan bills the same either way). /compact or start fresh; "+
			"you're re-reading a large cached context every turn.",
			cumStr, ceiling)
	case frac >= 0.75-fracEpsilon:
		return fmt.Sprintf("tokenops: %s API-equivalent this session, %d%% of %s "+
			"(not a charge). Consider /compact or a fresh session soon — cache-read grows "+
			"every turn you carry this context.",
			cumStr, pct, ceiling)
	default:
		return fmt.Sprintf("tokenops: %s API-equivalent this session, %d%% of %s — "+
			"mostly cache-read, and not a charge. A /compact resets the cached context.",
			cumStr, pct, ceiling)
	}
}

// formatUSD renders a whole-dollar budget without a trailing ".00" ("$50") and
// keeps cents only when present ("$50.50").
func formatUSD(v float64) string {
	if v == math.Trunc(v) {
		return fmt.Sprintf("$%d", int64(v))
	}
	return fmt.Sprintf("$%.2f", v)
}

// contextNote reports how full the model's context window is.
//
// This is the number that matters on a flat-rate plan. The dollar figure
// beside it is a counterfactual — the operator is billed the same either
// way — but context genuinely fills up: it forces a compaction, and until
// then every turn re-reads the whole of it. An operator whose sessions sit
// at 87% of a 1M window is paying for that on every single turn, and no
// amount of dollar framing makes that visible.
//
// An unknown model yields no percentage. A share computed against a
// guessed denominator looks authoritative and is not.
func contextNote(contextTokens int64, model string) string {
	if contextTokens <= 0 {
		return ""
	}
	window, known := spend.ContextWindow(model)
	if !known || window <= 0 {
		return fmt.Sprintf("Context: %s in the window (no published size for %s).",
			formatTokens(contextTokens), model)
	}
	pct := int(math.Round(float64(contextTokens) / float64(window) * 100))
	base := fmt.Sprintf("Context: %s of %s (%d%%)",
		formatTokens(contextTokens), formatTokens(window), pct)

	switch {
	case pct >= 90:
		return base + " — compact now; an automatic compaction is close and it will " +
			"choose what to drop for you."
	case pct >= 75:
		return base + " — worth compacting: every turn now re-reads this whole context."
	case pct >= 50:
		return base + "."
	default:
		return base + "."
	}
}

// formatTokens renders a token count compactly.
func formatTokens(n int64) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	case n >= 1_000:
		return fmt.Sprintf("%dk", n/1_000)
	default:
		return fmt.Sprintf("%d", n)
	}
}

// tipKind names the tip a Stop gave, or "" when it gave none or gave the
// read-guard case, which is not a tip about the session.
func tipKind(dec Decision) string {
	switch {
	case !dec.Nudge || dec.Promotion:
		return ""
	case dec.CompactTip:
		return compactKind
	case dec.QuotaTier > 0:
		return quotaKind(dec.QuotaTier)
	case dec.FiredFraction > 0:
		return budgetKind(dec.FiredFraction)
	}
	return ""
}
