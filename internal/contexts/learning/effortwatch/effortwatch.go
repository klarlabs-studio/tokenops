// Package effortwatch judges whether a change in the effort a model mostly
// runs at changed what its instructions cost, and whether they went as
// well, from the developer's own weekly series (ADR 0022). It is a pure
// judgement over weekly figures: it reads nothing and changes nothing.
//
// The change is observed, not made: the week the level most of a model's
// instructions ran at differs from the week before, whoever changed it.
// The comparison is before and after that week on one developer's
// machine, not a randomized holdout, so it says whether the developer's
// figures moved further than their own week-to-week movement, and cannot
// rule out other changes in the same weeks.
package effortwatch

import (
	"fmt"
	"math"
	"time"
)

// MinWeeks is how many counted weeks before the change, and from it, a
// judgement needs.
const MinWeeks = 4

// MinWeekInstructions is how many instructions a week needs to count: a
// week with fewer says little about the level or the rates.
const MinWeekInstructions = 10

// Week is one week of one model's instructions in one client.
type Week struct {
	Start        time.Time
	Instructions int
	// Dominant is the level most of the week's instructions ran at.
	Dominant string
	// USDPerInstruction is what one instruction cost at list prices; zero
	// when no cost was read, which leaves the week out.
	USDPerInstruction float64
	FirstTryPct       float64
	RejectedPct       float64
	InterruptedPct    float64
}

func (w Week) counted() bool {
	return w.Instructions >= MinWeekInstructions && w.Dominant != "" && w.USDPerInstruction > 0
}

// Change is the week the dominant level changed.
type Change struct {
	// At is the start of the first week at the new level.
	At   time.Time `json:"at"`
	From string    `json:"from"`
	To   string    `json:"to"`
}

// LatestChange is the most recent change of dominant level between two
// consecutive counted weeks; ok is false when the level never changed.
// weeks must be in time order.
func LatestChange(weeks []Week) (Change, bool) {
	counted := countedOf(weeks)
	for i := len(counted) - 1; i > 0; i-- {
		if counted[i].Dominant != counted[i-1].Dominant {
			return Change{At: counted[i].Start, From: counted[i-1].Dominant, To: counted[i].Dominant}, true
		}
	}
	return Change{}, false
}

func countedOf(weeks []Week) []Week {
	var out []Week
	for _, w := range weeks {
		if w.counted() {
			out = append(out, w)
		}
	}
	return out
}

// Interval is a weekly mean with its 95% interval, from the weeks'
// spread (Student's t, since there are only a few).
type Interval struct {
	Mean float64 `json:"mean"`
	Low  float64 `json:"low"`
	High float64 `json:"high"`
}

// Measure is one figure before and after the change.
type Measure struct {
	Before Interval `json:"before"`
	After  Interval `json:"after"`
	// Spread is the baseline's week-to-week movement: the mean absolute
	// change between consecutive weeks before the change.
	Spread float64 `json:"spread"`
	// Change is After less Before.
	Change float64 `json:"change"`
}

// Verdict is the judgement over one model's series.
type Verdict struct {
	Change Change `json:"change"`
	// Judged is false while either side has fewer than MinWeeks counted
	// weeks; Reason then says how many are still needed.
	Judged bool `json:"judged"`
	// Realized is true only when the cost per instruction fell by more
	// than its own week-to-week movement and no outcome rate worsened by
	// more than the margin.
	Realized bool `json:"realized"`
	// OutcomesWorse is true when an outcome rate worsened by more than
	// the margin, whatever the cost did.
	OutcomesWorse bool   `json:"outcomes_worse,omitempty"`
	Reason        string `json:"reason"`

	Cost        Measure `json:"cost_per_instruction"`
	FirstTry    Measure `json:"first_try_pct"`
	Rejected    Measure `json:"rejected_pct"`
	Interrupted Measure `json:"interrupted_pct"`
	// WeeklyInstructionsAfter is the mean instructions a week after the
	// change, for scaling the saving.
	WeeklyInstructionsAfter float64 `json:"weekly_instructions_after"`
}

// Judge compares the last MinWeeks counted weeks before c with the first
// MinWeeks from it. margin is how far, in percentage points, an outcome
// rate may worsen and still count as not worse.
func Judge(weeks []Week, c Change, margin float64) Verdict {
	var before, after []Week
	for _, w := range countedOf(weeks) {
		if w.Start.Before(c.At) {
			before = append(before, w)
		} else {
			after = append(after, w)
		}
	}
	v := Verdict{Change: c}
	if len(before) < MinWeeks {
		v.Reason = fmt.Sprintf("waiting for %d more weeks before the change; have %d", MinWeeks-len(before), len(before))
		return v
	}
	if len(after) < MinWeeks {
		v.Reason = fmt.Sprintf("waiting for %d more weeks since the change; have %d", MinWeeks-len(after), len(after))
		return v
	}
	before, after = before[len(before)-MinWeeks:], after[:MinWeeks]
	v.Judged = true
	v.Cost = measure(before, after, func(w Week) float64 { return w.USDPerInstruction })
	v.FirstTry = measure(before, after, func(w Week) float64 { return w.FirstTryPct })
	v.Rejected = measure(before, after, func(w Week) float64 { return w.RejectedPct })
	v.Interrupted = measure(before, after, func(w Week) float64 { return w.InterruptedPct })
	v.WeeklyInstructionsAfter = mean(after, func(w Week) float64 { return float64(w.Instructions) })

	v.OutcomesWorse = -v.FirstTry.Change > margin || v.Rejected.Change > margin || v.Interrupted.Change > margin
	costFell := -v.Cost.Change > v.Cost.Spread
	switch {
	case v.OutcomesWorse:
		v.Reason = fmt.Sprintf("an outcome rate worsened by more than %.0f points after the change", margin)
	case !costFell:
		v.Reason = "the cost per instruction did not fall beyond its week-to-week movement before the change"
	default:
		v.Realized = true
		v.Reason = fmt.Sprintf("the cost per instruction fell beyond its week-to-week movement, and no outcome rate worsened by more than %.0f points", margin)
	}
	return v
}

func measure(before, after []Week, f func(Week) float64) Measure {
	m := Measure{Before: interval(before, f), After: interval(after, f), Spread: spreadOf(before, f)}
	m.Change = m.After.Mean - m.Before.Mean
	return m
}

// tCrit is Student's two-sided 95% critical value by degrees of freedom.
var tCrit = map[int]float64{1: 12.706, 2: 4.303, 3: 3.182, 4: 2.776, 5: 2.571, 6: 2.447, 7: 2.365, 8: 2.306, 9: 2.262}

func interval(weeks []Week, f func(Week) float64) Interval {
	m := mean(weeks, f)
	n := len(weeks)
	if n < 2 {
		return Interval{Mean: m, Low: m, High: m}
	}
	var ss float64
	for _, w := range weeks {
		d := f(w) - m
		ss += d * d
	}
	t, ok := tCrit[n-1]
	if !ok {
		t = 1.96
	}
	half := t * math.Sqrt(ss/float64(n-1)) / math.Sqrt(float64(n))
	return Interval{Mean: m, Low: m - half, High: m + half}
}

// spreadOf is the mean absolute change between consecutive weeks.
func spreadOf(weeks []Week, f func(Week) float64) float64 {
	if len(weeks) < 2 {
		return math.Inf(1)
	}
	var sum float64
	for i := 1; i < len(weeks); i++ {
		sum += math.Abs(f(weeks[i]) - f(weeks[i-1]))
	}
	return sum / float64(len(weeks)-1)
}

func mean(weeks []Week, f func(Week) float64) float64 {
	if len(weeks) == 0 {
		return 0
	}
	var sum float64
	for _, w := range weeks {
		sum += f(w)
	}
	return sum / float64(len(weeks))
}
