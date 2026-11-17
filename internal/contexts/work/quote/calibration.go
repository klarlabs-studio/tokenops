package quote

import (
	"fmt"
	"math"
)

// MinScored is the fewest quoted tasks a calibration is shown for: below
// it, one task in or out of the p90 moves the share by ten points.
const MinScored = 10

// Scored is one quoted task set against what it took: the quote's p90
// and the actual, in one unit (dollars, or points of a window).
type Scored struct {
	P90    float64 `json:"p90"`
	Actual float64 `json:"actual"`
}

// Within reports whether the task stayed within the quote's p90.
func (s Scored) Within() bool { return s.Actual <= s.P90 }

// Calibration is how often quoted tasks stayed within their p90: about
// nine in ten when the quotes are honest.
type Calibration struct {
	Scored       int     `json:"scored"`
	MinScored    int     `json:"min_scored"`
	Enough       bool    `json:"enough"`
	WithinP90    int     `json:"within_p90"`
	WithinP90Pct float64 `json:"within_p90_pct"`
	Note         string  `json:"note"`
}

// Calibrate reads how often the scored tasks stayed within their p90.
func Calibrate(scored []Scored) Calibration {
	c := Calibration{Scored: len(scored), MinScored: MinScored}
	for _, s := range scored {
		if s.Within() {
			c.WithinP90++
		}
	}
	if c.Scored > 0 {
		c.WithinP90Pct = math.Round(float64(c.WithinP90)/float64(c.Scored)*1000) / 10
	}
	c.Enough = c.Scored >= MinScored
	switch {
	case c.Scored == 0:
		c.Note = "no quoted task has been reviewed yet; calibration shows once " + fmt.Sprint(MinScored) + " have"
	case !c.Enough:
		c.Note = fmt.Sprintf("%d of %d quoted tasks reviewed so far; calibration shows once %d have", c.Scored, MinScored, MinScored)
	case c.WithinP90Pct < 80:
		c.Note = fmt.Sprintf("the p90 runs low: %d of %d quoted tasks (%s%%) stayed within it, against about 90%% for an honest p90", c.WithinP90, c.Scored, pct(c.WithinP90Pct))
	default:
		c.Note = fmt.Sprintf("%d of %d quoted tasks (%s%%) stayed within their p90, against about 90%% for an honest p90", c.WithinP90, c.Scored, pct(c.WithinP90Pct))
	}
	return c
}
