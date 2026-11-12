package forecast

import (
	"math"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/observability/analytics"
)

// day is the step of a daily series.
const day = 24 * time.Hour

// Daily is the series a daily forecast is made from: one point for each
// whole UTC day, oldest first, ending with the day before Now's.
//
// A day with no rows is a point at zero, because nothing was spent on it:
// left out, the days either side of it read as consecutive and a forecast
// counts every day as a spending day. Now's own day is not in the series,
// because it is still running: read as a whole day, a morning's total
// reads as a day on which spend collapsed.
type Daily struct {
	Points []Point
	// Now is the time the series was read at. Its day is the first one a
	// forecast covers.
	Now time.Time
}

// Zero reports whether nothing was recorded on any day of the series, as
// in the dollar series of a flat-rate plan.
func (d Daily) Zero() bool {
	for _, p := range d.Points {
		if p.Value != 0 {
			return false
		}
	}
	return true
}

// TotalTokens is a row's token total, for DailyFrom.
func TotalTokens(r analytics.Row) float64 { return float64(r.TotalTokens) }

// CostUSD is a row's spend, for DailyFrom.
func CostUSD(r analytics.Row) float64 { return r.CostUSD }

// DailyFrom builds the series from rows bucketed by day and read from
// since on (the zero time when the read had no start). The series begins
// with the first day that has a row. The day since falls in was read in
// part unless since is its midnight, so it is left out like today.
func DailyFrom(rows []analytics.Row, get func(analytics.Row) float64, since, now time.Time) Daily {
	d := Daily{Now: now}
	if get == nil {
		return d
	}
	today := startOfDay(now)
	var from time.Time
	if !since.IsZero() {
		if from = startOfDay(since); from.Before(since) {
			from = from.Add(day)
		}
	}
	byDay := map[int64]float64{}
	var first time.Time
	for _, r := range rows {
		at := startOfDay(r.BucketStart)
		if at.Before(from) || !at.Before(today) {
			continue
		}
		byDay[at.Unix()] += get(r)
		if first.IsZero() || at.Before(first) {
			first = at
		}
	}
	if first.IsZero() {
		return d
	}
	for at := first; at.Before(today); at = at.Add(day) {
		d.Points = append(d.Points, Point{At: at, Value: byDay[at.Unix()]})
	}
	return d
}

func startOfDay(t time.Time) time.Time { return t.UTC().Truncate(day) }

// MinDays is the fewest whole days a Daily forecasts from.
const MinDays = 2

// weekdayDays is how many days a series must hold before each weekday may
// be forecast from its own days: two of every weekday.
const weekdayDays = 14

// outlook is what a Daily expects of a day ahead.
//
// A day ahead is expected to bring the mean of the days, never a trend
// through them. Spend is intermittent: bursts of work with days of nothing
// between them, and the days off each week. A trend reads each burst as a
// climb and each quiet stretch as a fall, and forecasts nothing, less than
// nothing, or a multiple of the spend, depending on the day it is asked.
// The mean of the days, the empty ones counted, is what a day brings on
// average, which is what an intermittent-demand method such as Croston's
// estimates too, without its bias.
//
// Spend that follows the week (work stops on the days off and resumes
// after them) is expected to bring the mean of the same weekday instead,
// once the series holds two of each, and only when the weekdays explain
// the days better than one mean day: when their spread about the
// weekday means, per day not fitted, is the smaller. Bursts that fall on
// any weekday do not follow the week, and read by weekday they forecast
// nothing on most days and a share of a burst on the others. sigma is the
// spread of the days about the means chosen.
type outlook struct {
	byWeekday [7]float64
	sigma     float64
}

// outlook reads the series; ok is false when it is shorter than MinDays.
func (d Daily) outlook() (outlook, bool) {
	n := len(d.Points)
	if n < MinDays {
		return outlook{}, false
	}
	var (
		sums   [7]float64
		counts [7]int
		total  float64
	)
	for _, p := range d.Points {
		wd := p.At.UTC().Weekday()
		sums[wd] += p.Value
		counts[wd]++
		total += p.Value
	}
	var o outlook
	for wd := range o.byWeekday {
		o.byWeekday[wd] = total / float64(n)
	}
	o.sigma = d.spread(o.byWeekday, 1)
	if n < weekdayDays {
		return o, true
	}
	var byWeekday [7]float64
	for wd := range byWeekday {
		byWeekday[wd] = sums[wd] / float64(counts[wd])
	}
	if sigma := d.spread(byWeekday, len(byWeekday)); sigma < o.sigma {
		o.byWeekday, o.sigma = byWeekday, sigma
	}
	return o, true
}

// spread is the residual standard error of the days about the means of
// their weekdays, of which fitted were fitted from the days.
func (d Daily) spread(byWeekday [7]float64, fitted int) float64 {
	n := len(d.Points)
	if n <= fitted {
		return 0
	}
	var ss float64
	for _, p := range d.Points {
		diff := p.Value - byWeekday[p.At.UTC().Weekday()]
		ss += diff * diff
	}
	return math.Sqrt(ss / float64(n-fitted))
}

// on is the forecast for the whole UTC day that starts at at.
func (o outlook) on(at time.Time) Prediction {
	v := o.byWeekday[at.UTC().Weekday()]
	ci := ConfidenceZ95 * o.sigma
	return Prediction{At: at, Value: v, Lower: math.Max(0, v-ci), Upper: v + ci}
}

// Ahead forecasts the horizon whole days that begin at or after Now.
// Today is left out unless Now is its midnight: part of it has already
// happened.
func (d Daily) Ahead(horizon int) []Prediction {
	o, ok := d.outlook()
	if !ok || horizon <= 0 {
		return nil
	}
	first := startOfDay(d.Now)
	if d.Now.After(first) {
		first = first.Add(day)
	}
	out := make([]Prediction, horizon)
	for i := range out {
		out[i] = o.on(first.Add(day * time.Duration(i)))
	}
	return out
}

// Rest forecasts what is left of a period that ends at end: the rest of
// Now's day, then each day up to end. A day that lies only partly in what
// is left counts for that share of its forecast, band included, so the
// rest of today is not a whole day on top of what today already spent,
// and nothing is counted past end. Each part is dated at the start of
// what it counts: Now for the rest of today.
func (d Daily) Rest(end time.Time) []Prediction {
	o, ok := d.outlook()
	if !ok || !end.After(d.Now) {
		return nil
	}
	var out []Prediction
	for at := startOfDay(d.Now); at.Before(end); at = at.Add(day) {
		from, to := at, at.Add(day)
		if from.Before(d.Now) {
			from = d.Now
		}
		if to.After(end) {
			to = end
		}
		share := float64(to.Sub(from)) / float64(day)
		p := o.on(at)
		out = append(out, Prediction{At: from, Value: p.Value * share, Lower: p.Lower * share, Upper: p.Upper * share})
	}
	return out
}
