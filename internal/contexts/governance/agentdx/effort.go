package agentdx

import (
	"sort"
	"strings"
)

// EffortRow is how instructions went at one model and reasoning effort.
//
// Effort is the second dial on the same decision as the model: a lookup
// at high effort on a flagship model may be better served by lowering the
// effort than by switching models, and lowering effort keeps the prompt
// cache that a model switch throws away. Whether it pays is an empirical
// question per operator, which is what these rows are for.
//
// The rows compare observations, not experiments: operators raise effort
// for harder work, so a higher effort level doing worse is at least as
// likely to mean harder instructions as a worse setting. Compare levels
// within one model, and only where both have enough instructions.
type EffortRow struct {
	Model        string `json:"model"`
	Effort       string `json:"effort"`
	Instructions int    `json:"instructions"`
	// Enough is false below MinEffortInstructions; such a row is shown
	// for completeness and not for comparison.
	Enough bool `json:"enough"`

	MedianTurns   float64 `json:"median_turns"`
	MedianSeconds float64 `json:"median_seconds"`
	// MedianPeakContext is the largest single turn's context, the honest
	// "how heavy was it" figure.
	MedianPeakContext int64 `json:"median_peak_context"`
	// MedianCarriedTokens is the context summed across the unit's turns.
	// It double-counts by design (every turn re-sends the context) and is
	// for comparing rows, never a spend figure.
	MedianCarriedTokens int64 `json:"median_carried_tokens"`

	FirstTryPct    float64 `json:"first_try_pct"`
	RejectedPct    float64 `json:"rejected_pct"`
	InterruptedPct float64 `json:"interrupted_pct"`
}

// MinEffortInstructions is the floor for comparing an effort row.
const MinEffortInstructions = 30

// effortOrder ranks the levels clients use, lowest first; unknown levels
// sort after them by name.
var effortOrder = map[string]int{
	"none": 0, "minimal": 1, "low": 2, "medium": 3, "high": 4, "xhigh": 5, "max": 6,
}

// ByEffort groups instructions by the model and effort that served most
// of their turns. Units are built per session, so concurrent sessions do
// not lend each other turns. Instructions with no recorded effort are
// left out: the client did not say, and guessing would put them in a row.
func ByEffort(records []Record) []EffortRow {
	bySession := map[string][]Record{}
	for _, r := range records {
		bySession[r.SessionID] = append(bySession[r.SessionID], r)
	}
	groups := map[[2]string][]Unit{}
	for _, recs := range bySession {
		for _, u := range Units(recs) {
			if u.Effort == "" || u.Turns == 0 {
				continue
			}
			k := [2]string{u.Model, u.Effort}
			groups[k] = append(groups[k], u)
		}
	}
	rows := make([]EffortRow, 0, len(groups))
	for k, units := range groups {
		rows = append(rows, effortRow(k[0], k[1], units))
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Model != rows[j].Model {
			return rows[i].Model < rows[j].Model
		}
		return effortLess(rows[i].Effort, rows[j].Effort)
	})
	return rows
}

func effortRow(model, effort string, units []Unit) EffortRow {
	row := EffortRow{Model: model, Effort: effort, Instructions: len(units)}
	row.Enough = row.Instructions >= MinEffortInstructions
	turns := make([]float64, 0, len(units))
	var seconds, peak, carried []float64
	var firstTry, rejected, interrupted int
	for _, u := range units {
		turns = append(turns, float64(u.Turns))
		if d := u.Duration(); d > 0 {
			seconds = append(seconds, d.Seconds())
		}
		if u.PeakContext > 0 {
			peak = append(peak, float64(u.PeakContext))
		}
		if u.Tokens > 0 {
			carried = append(carried, float64(u.Tokens))
		}
		if u.FirstTry() {
			firstTry++
		}
		if u.Rejected {
			rejected++
		}
		if u.Interrupted {
			interrupted++
		}
	}
	n := float64(len(units))
	row.MedianTurns = round1(percentile(turns, 0.5))
	row.MedianSeconds = round1(percentile(seconds, 0.5))
	row.MedianPeakContext = int64(percentile(peak, 0.5))
	row.MedianCarriedTokens = int64(percentile(carried, 0.5))
	row.FirstTryPct = round1(float64(firstTry) / n * 100)
	row.RejectedPct = round1(float64(rejected) / n * 100)
	row.InterruptedPct = round1(float64(interrupted) / n * 100)
	return row
}

func effortLess(a, b string) bool {
	ra, oka := effortOrder[a]
	rb, okb := effortOrder[b]
	switch {
	case oka && okb:
		return ra < rb
	case oka != okb:
		return oka
	default:
		return strings.Compare(a, b) < 0
	}
}
