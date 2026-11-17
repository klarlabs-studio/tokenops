package plans

import (
	"fmt"
	"math"
)

// ValueTier is plan as the plan in force of a value-only advice, priced
// at its catalog list price: a catalog plan with a fee whose tiers state
// no usage multiple of each other (Cursor, Copilot, ChatGPT Pro with its
// tier unspecified), so its windows cannot be replayed against them. ok is
// false for a plan the replay handles, one billed per token, the unnamed
// Subscription, and any name the catalog does not know.
func ValueTier(name string) (Tier, bool) {
	if name == Subscription {
		return Tier{}, false
	}
	p, known := Lookup(name)
	if !known || p.SpendDenominated {
		return Tier{}, false
	}
	if _, _, replayable := tierOf(name); replayable {
		return Tier{}, false
	}
	t := Tier{Name: p.Name, Display: p.Display, NoSessionLimit: p.NoSessionLimit, MonthlyUSD: p.MonthlyUSD}
	if t.MonthlyUSD > 0 {
		t.PriceSource = "list"
	}
	return t, true
}

// ValueOnly is the advice for a plan whose tiers cannot be replayed
// (ValueTier): the plan-covered usage at the underlying models' API list
// price over the days of the period the event history covers, and that as
// a multiple of the fee for those days. Both are estimates of what the
// usage would have cost per token, not what the vendor charges. With no
// tier to compare and no limit measured, it never moves a tier or cancels
// the plan: the recommendation is to stay. Nothing is recommended when
// the usage cannot be priced.
func ValueOnly(in ReplayInput) Advice {
	a := Advice{Provider: in.Provider, Plan: in.Current.Name, Display: in.Current.Display,
		Since: in.Since, Until: in.Until, Confidence: ConfidenceLow, ValueOnly: true}
	if in.ListValue == nil || !in.Until.After(in.Since) {
		return a
	}
	from := in.historyStart()
	a.Days = math.Round(in.Until.Sub(from).Hours()/24*10) / 10
	if a.Days <= 0 {
		a.Days = 0.1
	}
	v := valueOver(in, from)
	setListValue(&a, v, in.Current.MonthlyUSD)
	cur := Option{Kind: OptionPlan, Plan: in.Current.Name, Display: in.Current.Display, Current: true,
		FeeUSD: in.Current.MonthlyUSD, PriceSource: in.Current.PriceSource}
	if in.Current.MonthlyUSD > 0 {
		cur.MonthlyUSD, cur.SavingsUSD = ptrFloat(round2(in.Current.MonthlyUSD)), ptrFloat(0)
	}
	a.Options = []Option{cur}
	a.Caveats = append(a.Caveats,
		"An estimate: the usage this plan covered at the underlying models' API list price, not what "+in.Current.Display+" charges for it.",
		"The catalog states no usage multiple between this plan's tiers, so no other tier is compared, "+
			"and with no limit measured no cancellation is suggested.",
		"Usage TokenOps does not see (a web chat, another machine) is not in it.")
	if v.unpriced > 0 {
		a.Caveats = append(a.Caveats, fmt.Sprintf("%d request(s) used models with no API rate, so the value understates them.", v.unpriced))
	}
	monthly := round2(v.usd * averageMonthDays / a.Days)
	summary := fmt.Sprintf("Keep %s: your usage would have cost about $%.0f/mo at the underlying models' API list price (an estimate)",
		in.Current.Display, monthly)
	if a.ValueMultiple != nil {
		summary += fmt.Sprintf(", %.1fx its $%.0f/mo fee.", *a.ValueMultiple, in.Current.MonthlyUSD)
	} else {
		summary += "; with no price for the plan there is no multiple of its fee."
	}
	a.Recommendation = &Recommendation{Verdict: VerdictStay, Kind: OptionPlan, Plan: in.Current.Name,
		Display: in.Current.Display, Summary: summary}
	return a
}
