// Package forecast forecasts daily spend and token use from the operator's
// own history.
//
// Daily is the series every forecast is made from: whole UTC days, a day
// with no usage counted at zero, today (still running) left out. A day
// ahead is expected to bring the mean of the days, or of its weekday when
// the spend follows the week, never a trend through them: spend is
// intermittent, and a trend method (Holt's, a regression line) reads each
// burst as a climb and each quiet stretch as a fall.
//
// Each prediction carries a 95% band from the spread of the days about
// what was expected of them, cut at zero.
package forecast

import "time"

// Point is one (timestamp, value) sample of a series.
type Point struct {
	At    time.Time
	Value float64
}

// Prediction is one predicted value with its 95% band: Lower ≤ Value ≤
// Upper, Lower never below zero.
type Prediction struct {
	At    time.Time
	Value float64
	Lower float64
	Upper float64
}

// ConfidenceZ95 is the normal-approximation factor for a 95% band.
const ConfidenceZ95 = 1.96
