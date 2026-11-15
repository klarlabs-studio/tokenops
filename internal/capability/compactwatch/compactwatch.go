// Package compactwatch judges whether setting the compaction line from the
// developer's own context line saved anything, from the developer's own
// weekly series (ADR 0021). It is a pure judgement over weekly figures: it
// reads nothing and changes nothing.
//
// The comparison is before and after one setting on one developer's
// machine, not a randomized holdout: the setting is one for everyone's
// own work, so the question is whether this developer's measures moved
// further than their own week-to-week movement would explain.
package compactwatch

import (
	"fmt"
	"math"
	"time"
)

// MinWeeks is how many whole weeks before the setting, and after it, a
// judgement needs.
const MinWeeks = 4

// Week is one week of the developer's own figures.
type Week struct {
	Start time.Time
	// OversizedTokens is the context carried above the compaction line in
	// the week: the waste class oversized_context.
	OversizedTokens int64
	// RejectRatePct is the share of the week's instructions the developer
	// rejected, in the bands with enough instructions to judge.
	RejectRatePct float64
}

// Measure is one figure's comparison.
type Measure struct {
	// Before and After are the weekly means either side of the setting.
	Before float64
	After  float64
	// Spread is the baseline's week-to-week movement: the mean absolute
	// change between consecutive weeks before the setting. A drop is only
	// a saving when it is larger than this.
	Spread float64
	// Drop is Before minus After; positive when the figure fell.
	Drop float64
}

// Verdict is the judgement over a series.
type Verdict struct {
	// Realized is true only when both figures fell by more than the
	// baseline's own week-to-week movement.
	Realized bool
	// Reason says, in one plain sentence, why it is or is not realized,
	// or how many weeks are still needed.
	Reason    string
	Oversized Measure
	Rejects   Measure
}

// Judge compares the weeks before setting with the weeks from it. It takes
// the last minWeeks before the setting and the first minWeeks from it; with
// fewer on either side it says how many it is waiting for.
func Judge(weeks []Week, setting time.Time, minWeeks int) Verdict {
	var before, after []Week
	for _, w := range weeks {
		if w.Start.Before(setting) {
			before = append(before, w)
		} else {
			after = append(after, w)
		}
	}
	if len(before) < minWeeks {
		return Verdict{Reason: fmt.Sprintf("waiting for %d more weeks before the setting; have %d", minWeeks-len(before), len(before))}
	}
	if len(after) < minWeeks {
		return Verdict{Reason: fmt.Sprintf("waiting for %d more weeks since the setting; have %d", minWeeks-len(after), len(after))}
	}
	before = before[len(before)-minWeeks:]
	after = after[:minWeeks]

	v := Verdict{
		Oversized: measure(before, after, tokensOf),
		Rejects:   measure(before, after, rejectsOf),
	}
	switch {
	case v.Oversized.Drop <= v.Oversized.Spread:
		v.Reason = "the context carried above the line did not fall beyond the week-to-week movement before the setting"
	case v.Rejects.Drop <= v.Rejects.Spread:
		v.Reason = "the context fell, but the reject rate did not fall beyond its week-to-week movement"
	default:
		v.Realized = true
		v.Reason = "the context carried above the line and the reject rate both fell beyond the week-to-week movement before the setting"
	}
	return v
}

func measure(before, after []Week, f func(Week) float64) Measure {
	m := Measure{Before: mean(before, f), After: mean(after, f), Spread: spreadOf(before, f)}
	m.Drop = m.Before - m.After
	return m
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
	var sum float64
	for _, w := range weeks {
		sum += f(w)
	}
	return sum / float64(len(weeks))
}

func tokensOf(w Week) float64  { return float64(w.OversizedTokens) }
func rejectsOf(w Week) float64 { return w.RejectRatePct }
