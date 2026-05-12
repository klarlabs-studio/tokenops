package plans

import (
	"fmt"
	"math"
	"time"
)

// HeadroomReport summarises a single plan's monthly consumption. It is
// the canonical wire shape for the `tokenops plan` CLI surface and the
// `tokenops_plan_headroom` MCP tool.
type HeadroomReport struct {
	PlanName       string  `json:"plan_name"`
	Display        string  `json:"display"`
	Provider       string  `json:"provider"`
	QuotaTokens    int64   `json:"quota_tokens"`
	ConsumedTokens int64   `json:"consumed_tokens"`
	ConsumedPct    float64 `json:"consumed_pct"`
	HeadroomDays   float64 `json:"headroom_days"`
	OverageRisk    string  `json:"overage_risk"`
	// Note explains the report when the math falls through to a
	// special case — quota not published, insufficient burn history,
	// already past the cap. Empty when the headline numbers are
	// authoritative.
	Note string `json:"note,omitempty"`
}

// Risk levels used by HeadroomReport.OverageRisk. Closed set so
// dashboards can colour-map without consulting the catalog.
const (
	RiskLow     = "low"
	RiskMedium  = "medium"
	RiskHigh    = "high"
	RiskUnknown = "unknown"
)

// HeadroomInputs captures the live counters the engine feeds into the
// headroom calculator. KEEPS the math pure: tests pass arbitrary
// scenarios without needing a sqlite store.
type HeadroomInputs struct {
	// ConsumedTokens is the total tokens spent against the plan since
	// the start of the current billing month.
	ConsumedTokens int64
	// Last7DayTokens is the rolling sum used to project burn rate.
	// Zero means insufficient history; the report falls back to a
	// note rather than fabricating a horizon.
	Last7DayTokens int64
	// Now is the clock reference. Tests inject a fixed time; production
	// passes time.Now().UTC().
	Now time.Time
}

// ComputeHeadroom builds a HeadroomReport for the named plan from the
// supplied inputs. An unknown plan name returns an error so callers
// surface the typo instead of silently zeroing the dashboard.
func ComputeHeadroom(planName string, in HeadroomInputs) (HeadroomReport, error) {
	p, ok := Lookup(planName)
	if !ok {
		return HeadroomReport{}, fmt.Errorf("unknown plan %q", planName)
	}
	return computeHeadroomFor(p, in), nil
}

// computeHeadroomFor is the pure-Plan variant; ComputeHeadroom does the
// catalog lookup then delegates here. Split out so unit tests can drive
// arbitrary plan shapes (with / without token quotas) without polluting
// the public catalog.
func computeHeadroomFor(p Plan, in HeadroomInputs) HeadroomReport {
	report := HeadroomReport{
		PlanName:       p.Name,
		Display:        p.Display,
		Provider:       p.Provider,
		QuotaTokens:    p.InputTokensPerMonth + p.OutputTokensPerMonth,
		ConsumedTokens: in.ConsumedTokens,
		OverageRisk:    RiskUnknown,
	}

	if report.QuotaTokens <= 0 {
		report.Note = "no monthly token cap published; rate-limit window applies"
		return report
	}

	report.ConsumedPct = math.Round(float64(report.ConsumedTokens)/float64(report.QuotaTokens)*10000) / 100

	daysLeftInMonth := daysRemainingInMonth(in.Now)
	if in.Last7DayTokens <= 0 {
		report.Note = "insufficient burn history; need ≥7d of plan-included traffic"
		report.HeadroomDays = math.NaN()
		report.OverageRisk = classifyRisk(report.ConsumedPct, math.NaN(), daysLeftInMonth)
		return report
	}

	dailyBurn := float64(in.Last7DayTokens) / 7.0
	remaining := report.QuotaTokens - report.ConsumedTokens
	if remaining <= 0 {
		report.HeadroomDays = 0
		report.OverageRisk = RiskHigh
		report.Note = "monthly quota exhausted"
		return report
	}
	report.HeadroomDays = math.Round(float64(remaining)/dailyBurn*10) / 10
	report.OverageRisk = classifyRisk(report.ConsumedPct, report.HeadroomDays, daysLeftInMonth)
	return report
}

// classifyRisk encodes the three thresholds the operator surface
// renders. High = >=80% consumed AND headroom shorter than the billing
// month remainder; medium = >=60% consumed or headroom < 1.5x month
// remainder; otherwise low. NaN headroom (no history) collapses to
// unknown unless consumption alone is already alarming.
func classifyRisk(consumedPct, headroomDays, daysLeftInMonth float64) string {
	if math.IsNaN(headroomDays) {
		switch {
		case consumedPct >= 80:
			return RiskHigh
		case consumedPct >= 60:
			return RiskMedium
		default:
			return RiskUnknown
		}
	}
	switch {
	case consumedPct >= 80 && headroomDays < daysLeftInMonth:
		return RiskHigh
	case consumedPct >= 60 || headroomDays < daysLeftInMonth*1.5:
		return RiskMedium
	default:
		return RiskLow
	}
}

// daysRemainingInMonth returns the number of full days left in the
// calendar month containing now (UTC). Used as the comparison window
// for headroom vs. burn extrapolation.
func daysRemainingInMonth(now time.Time) float64 {
	now = now.UTC()
	firstOfNext := time.Date(now.Year(), now.Month()+1, 1, 0, 0, 0, 0, time.UTC)
	return firstOfNext.Sub(now).Hours() / 24
}
