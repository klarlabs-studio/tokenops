package agentdx

import (
	"fmt"
	"math"
)

// Effort is a cost lever: a model's price per token does not depend on the
// effort it runs at, but the tokens do, since reasoning is billed as
// output. Where the developer's own instructions at a lower level went as
// well as at the level they mostly use, and cost materially less, the
// lower level is worth suggesting as the default for that model.

// EffortOutcomeMargin is how far, in percentage points, a lower level's
// first-try, rejected and interrupted rates may trail the current level's
// and still count as not worse. At MinEffortInstructions a rate moves
// about this much on a handful of instructions, so a smaller gap is not a
// difference the rows can show.
const EffortOutcomeMargin = 5.0

// MinEffortCostDrop is the share by which a lower level's cost per
// instruction must fall below the current level's for the move to be
// worth suggesting.
const MinEffortCostDrop = 0.25

// daysPerMonth scales a window's saving to a month.
const daysPerMonth = 30.0

// EffortConfound is the caveat every effort suggestion carries.
const EffortConfound = "This is an observation, not an experiment: you raise effort for harder work, " +
	"so the instructions at the lower level may simply have been easier. " +
	"Only levels of the same model in the same client are compared."

// EffortSuggestion is a lower default effort for one model in one client.
type EffortSuggestion struct {
	Client string `json:"client"`
	Model  string `json:"model"`
	// From is the level most of the model's instructions ran at; To the
	// lower level suggested.
	From EffortRow `json:"from"`
	To   EffortRow `json:"to"`
	// CostDropPct is how much less one instruction cost at To.
	CostDropPct float64 `json:"cost_drop_pct"`
	// MonthlySavingUSD is an estimate: the instructions run at From in the
	// window, priced at the difference, scaled to 30 days. Zero when the
	// window has no length to scale from.
	MonthlySavingUSD float64 `json:"monthly_saving_usd,omitempty"`
	// Basis says how MonthlySavingUSD was worked out.
	Basis    string `json:"basis,omitempty"`
	Confound string `json:"confound"`
}

// SuggestEffort finds, per client and model, a lower effort level worth
// suggesting as the default. The current level is the one with the most
// instructions; a lower level qualifies when both have enough instructions
// to compare and a cost, its outcomes are within EffortOutcomeMargin of
// the current level's, and one instruction costs at least
// MinEffortCostDrop less. Of several, the cheapest is suggested. windowDays
// is the length the rows cover, for the monthly estimate; zero or less
// leaves the estimate out.
func SuggestEffort(rows []EffortRow, windowDays int) []EffortSuggestion {
	type modelKey struct{ client, model string }
	byModel := map[modelKey][]EffortRow{}
	var order []modelKey
	for _, r := range rows {
		k := modelKey{r.Client, r.Model}
		if _, seen := byModel[k]; !seen {
			order = append(order, k)
		}
		byModel[k] = append(byModel[k], r)
	}
	var out []EffortSuggestion
	for _, k := range order {
		if s, ok := suggestFor(byModel[k], windowDays); ok {
			out = append(out, s)
		}
	}
	return out
}

func suggestFor(rows []EffortRow, windowDays int) (EffortSuggestion, bool) {
	from, ok := dominant(rows)
	if !ok {
		return EffortSuggestion{}, false
	}
	var best EffortRow
	found := false
	for _, r := range rows {
		if !lowerKnown(r.Effort, from.Effort) || !comparable(r) || !notWorse(r, from) {
			continue
		}
		if r.USDPerInstruction > from.USDPerInstruction*(1-MinEffortCostDrop) {
			continue
		}
		if !found || r.USDPerInstruction < best.USDPerInstruction {
			best, found = r, true
		}
	}
	if !found {
		return EffortSuggestion{}, false
	}
	s := EffortSuggestion{
		Client: from.Client, Model: from.Model, From: from, To: best,
		CostDropPct: round1((1 - best.USDPerInstruction/from.USDPerInstruction) * 100),
		Confound:    EffortConfound,
	}
	if windowDays > 0 {
		perInstr := from.USDPerInstruction - best.USDPerInstruction
		s.MonthlySavingUSD = math.Round(perInstr*float64(from.Instructions)*daysPerMonth/float64(windowDays)*100) / 100
		s.Basis = fmt.Sprintf("estimate: your %d instructions at %s in %d days, each $%.2f cheaper at %s, scaled to 30 days at list prices",
			from.Instructions, from.Effort, windowDays, perInstr, best.Effort)
	}
	return s, true
}

// dominant is the level most of the model's instructions ran at, when it
// has enough of them and a cost.
func dominant(rows []EffortRow) (EffortRow, bool) {
	var top EffortRow
	for _, r := range rows {
		if r.Instructions > top.Instructions {
			top = r
		}
	}
	return top, comparable(top)
}

func comparable(r EffortRow) bool { return r.Enough && r.USDPerInstruction > 0 }

// lowerKnown reports whether a is a lower level than b, both being levels
// the clients name; an unknown level is not ranked.
func lowerKnown(a, b string) bool {
	ra, oka := effortOrder[a]
	rb, okb := effortOrder[b]
	return oka && okb && ra < rb
}

// notWorse reports whether r's outcomes are within the margin of base's.
func notWorse(r, base EffortRow) bool {
	return r.FirstTryPct >= base.FirstTryPct-EffortOutcomeMargin &&
		r.RejectedPct <= base.RejectedPct+EffortOutcomeMargin &&
		r.InterruptedPct <= base.InterruptedPct+EffortOutcomeMargin
}
