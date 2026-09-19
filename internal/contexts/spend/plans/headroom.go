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

	// Window* fields describe the rolling rate-limit window for plans
	// that publish one (Claude Max 5h, ChatGPT Plus 3h). Zero
	// WindowCap means the vendor does not publish a concrete cap.
	WindowDuration string    `json:"window_duration,omitempty"`
	WindowCap      int64     `json:"window_cap,omitempty"`
	WindowUnit     string    `json:"window_unit,omitempty"`
	WindowConsumed int64     `json:"window_consumed,omitempty"`
	WindowPct      float64   `json:"window_pct,omitempty"`
	WindowResetsAt time.Time `json:"window_resets_at,omitempty"`
	WindowResetsIn string    `json:"window_resets_in,omitempty"`
	// WindowResetEstimated is true when the reset was worked out from the
	// first activity in the window rather than reported by the vendor.
	WindowResetEstimated bool `json:"window_reset_estimated,omitempty"`

	// Spend* fields replace the window block for spend-denominated plans
	// (usage-based Enterprise), where the limit is money the org set
	// rather than a rate-limit window the vendor publishes.
	SpendLimitUSD float64 `json:"spend_limit_usd,omitempty"`
	SpendUSD      float64 `json:"spend_usd,omitempty"`
	SpendPct      float64 `json:"spend_pct,omitempty"`
	// SpendSource says where the spend figures came from: "vendor" when
	// the vendor reported both spend and limit, "estimate" when spend was
	// recomputed from token counts against the configured limit.
	SpendSource string `json:"spend_source,omitempty"`

	// SignalQuality types the trust the operator can place in this
	// report. When the report is built from MCP-ping activity only,
	// Level is "low" and the Caveat tells the consumer to treat it as
	// an activity proxy, not a quota meter. See ClassifySignal.
	SignalQuality SignalQuality `json:"signal_quality"`

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
	// WindowMessages is the count of plan-included messages observed
	// within Plan.RateLimitWindow. Drives the window-based headroom
	// metrics; zero is a valid "no traffic yet" reading.
	WindowMessages int64
	// Signal is the observation triple ClassifySignal needs to assign
	// a trust level. Zero value is valid: defaults to the most
	// pessimistic reading (MCP-pings only, low quality).
	Signal SignalInputs
	// SpendLimitUSD is the org spend limit the operator configured, and
	// SpendUSD the spend measured against it. Only spend-denominated
	// plans read these.
	SpendLimitUSD float64
	SpendUSD      float64
	// VendorSpend, when set, is the vendor's own spend and limit. It
	// replaces both SpendUSD and SpendLimitUSD: what the vendor bills
	// outranks a recomputation from a public rate card (ADR 0003).
	VendorSpend *VendorSpend
	// RateFactor scales measured spend to a negotiated rate. Enterprise
	// contracts are frequently discounted off list while this tool costs
	// from the public rate card, so without it a console limit is compared
	// against an overstatement — and the error is invisible. Zero or one
	// means list price.
	RateFactor float64
	// WindowStartedAt is the first plan-covered activity inside the
	// rate-limit window (WindowConsumption.FirstActivityAt). Without a
	// vendor-reported reset, the window is estimated to reset one window
	// length after it.
	WindowStartedAt time.Time
	// Authoritative, when set, is the vendor's own reported rate-limit
	// window % (see AuthoritativeWindow). It drives the Window* block
	// directly instead of the WindowMessages count — the same upgrade
	// ComputeSessionBudget makes — so the window dimension reflects the
	// vendor meter rather than an event count.
	Authoritative *AuthoritativeWindow
	// MonthlyAuthoritative, when set, is the vendor's own reported MONTHLY
	// quota % (Copilot percent_remaining, Cursor used_pct). It drives the
	// monthly ConsumedPct + overage risk directly, which is the only useful
	// monthly signal for request-quota plans that publish no token cap.
	MonthlyAuthoritative *AuthoritativeWindow
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
	if p.SpendDenominated {
		return computeSpendHeadroom(p, in)
	}
	report := HeadroomReport{
		PlanName:       p.Name,
		Display:        p.Display,
		Provider:       p.Provider,
		QuotaTokens:    p.InputTokensPerMonth + p.OutputTokensPerMonth,
		ConsumedTokens: in.ConsumedTokens,
		OverageRisk:    RiskUnknown,
		SignalQuality:  ClassifySignal(in.Signal),
	}

	// Rolling-window headroom (Claude Max 5h, ChatGPT Plus 3h). This
	// runs alongside the monthly path so plans with both surfaces
	// (rare today) get both metrics; plans with only a window get a
	// useful report instead of "no monthly cap published".
	var monthlyRisk string
	windowRisk := RiskUnknown
	switch {
	case in.Authoritative != nil && p.RateLimitWindow > 0:
		// Vendor meter wins — same upgrade as ComputeSessionBudget. Works
		// even for plans with no message cap (the % + reset carry it).
		windowRisk = applyAuthoritativeWindow(&report, p, in)
	case p.RateLimitWindow > 0 && p.MessagesPerWindow > 0:
		report.WindowDuration = p.RateLimitWindow.String()
		report.WindowCap = p.MessagesPerWindow
		report.WindowUnit = p.WindowUnit
		report.WindowConsumed = in.WindowMessages
		report.WindowPct = math.Round(float64(in.WindowMessages)/float64(p.MessagesPerWindow)*10000) / 100
		setEstimatedReset(&report, in.WindowStartedAt, p.RateLimitWindow, in.Now)
		windowRisk = classifyWindowRisk(report.WindowPct)
	}

	// Vendor monthly meter wins the monthly dimension when present — the
	// only useful monthly signal for request-quota plans (Copilot, Cursor)
	// that publish no token cap.
	if in.MonthlyAuthoritative != nil {
		return finishAuthoritativeMonthly(report, in, windowRisk)
	}

	if report.QuotaTokens <= 0 {
		// No monthly token cap — defer entirely to the window signal.
		if windowRisk != RiskUnknown {
			report.OverageRisk = windowRisk
		} else {
			report.Note = "no monthly token cap published; rate-limit window applies"
		}
		return report
	}

	report.ConsumedPct = math.Round(float64(report.ConsumedTokens)/float64(report.QuotaTokens)*10000) / 100

	daysLeftInMonth := daysRemainingInMonth(in.Now)
	if in.Last7DayTokens <= 0 {
		report.Note = "insufficient burn history; need ≥7d of plan-included traffic"
		report.HeadroomDays = math.NaN()
		monthlyRisk = classifyRisk(report.ConsumedPct, math.NaN(), daysLeftInMonth)
		report.OverageRisk = worstRisk(monthlyRisk, windowRisk)
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
	monthlyRisk = classifyRisk(report.ConsumedPct, report.HeadroomDays, daysLeftInMonth)
	report.OverageRisk = worstRisk(monthlyRisk, windowRisk)
	return report
}

// finishAuthoritativeMonthly builds the monthly dimension from the vendor's
// reported quota % instead of token counts. ConsumedPct comes straight from
// the meter; HeadroomDays reflects the time until the quota resets (when the
// vendor reports it). Works with no token cap — the % + reset carry it.
func finishAuthoritativeMonthly(report HeadroomReport, in HeadroomInputs, windowRisk string) HeadroomReport {
	a := in.MonthlyAuthoritative
	pct := clampPct(a.UsedPct)
	report.ConsumedPct = math.Round(pct*100) / 100
	if a.ResetsIn > 0 {
		report.HeadroomDays = math.Round(a.ResetsIn.Hours()/24*10) / 10
	} else {
		report.HeadroomDays = math.NaN()
	}
	monthlyRisk := classifyWindowRisk(pct)
	if monthlyRisk == RiskUnknown {
		// A real 0%-used reading is genuinely low risk, not unknown.
		monthlyRisk = RiskLow
	}
	report.OverageRisk = worstRisk(monthlyRisk, windowRisk)
	report.Note = "monthly % is the vendor's reported quota meter (" + a.Source + ")"
	return report
}

// applyAuthoritativeWindow fills report's Window* fields from the vendor's
// reported quota % + reset (see AuthoritativeWindow) and returns the window
// risk. Unlike the message-count path it works for plans with no message cap
// — the % and reset are the whole signal — so capless plans (Claude Code
// Max/Pro) finally get an authoritative window reading.
func applyAuthoritativeWindow(report *HeadroomReport, p Plan, in HeadroomInputs) string {
	a := in.Authoritative
	pct := clampPct(a.UsedPct)
	report.WindowDuration = p.RateLimitWindow.String()
	report.WindowCap = p.MessagesPerWindow
	report.WindowUnit = p.WindowUnit
	report.WindowPct = math.Round(pct*100) / 100
	if p.MessagesPerWindow > 0 {
		report.WindowConsumed = int64(math.Round(float64(p.MessagesPerWindow) * pct / 100))
	}
	if a.ResetsIn > 0 {
		report.WindowResetsAt = in.Now.Add(a.ResetsIn).UTC()
		report.WindowResetsIn = a.ResetsIn.Round(time.Minute).String()
	} else {
		setEstimatedReset(report, in.WindowStartedAt, p.RateLimitWindow, in.Now)
	}
	return classifyWindowRisk(report.WindowPct)
}

// classifyWindowRisk maps a rate-limit-window utilisation percentage to
// the standard risk levels. Thresholds mirror the monthly-quota
// classifier (80 = high, 60 = medium) so dashboards stay uniform.
func classifyWindowRisk(pct float64) string {
	switch {
	case pct >= 80:
		return RiskHigh
	case pct >= 60:
		return RiskMedium
	case pct > 0:
		return RiskLow
	default:
		return RiskUnknown
	}
}

// worstRisk returns the more alarming of two risk levels so the
// headline number reflects either dimension hitting trouble.
func worstRisk(a, b string) string {
	rank := map[string]int{RiskUnknown: 0, RiskLow: 1, RiskMedium: 2, RiskHigh: 3}
	if rank[a] >= rank[b] {
		return a
	}
	return b
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

// computeSpendHeadroom reports money against the org's own limit, for plans
// that are billed rather than rate-limited.
//
// It deliberately fills none of the Window* fields. A plan billed at API
// rates from the first token has no window, and rendering an empty one would
// invite the reader to believe there is a cap somewhere they are under.
func computeSpendHeadroom(p Plan, in HeadroomInputs) HeadroomReport {
	report := HeadroomReport{
		PlanName:      p.Name,
		Display:       p.Display,
		Provider:      p.Provider,
		OverageRisk:   RiskUnknown,
		SignalQuality: ClassifySignal(in.Signal),
	}
	if v := in.VendorSpend; v != nil {
		// The vendor's own figures: the amount it will bill against the
		// limit it enforces. No rate factor — they are already the rate.
		report.SpendUSD, report.SpendLimitUSD, report.SpendSource = v.UsedUSD, v.LimitUSD, "vendor"
		report.SpendPct = math.Round(v.UsedUSD/v.LimitUSD*10000) / 100
		report.OverageRisk = classifyWindowRisk(report.SpendPct)
		if v.LimitReached {
			report.OverageRisk = RiskHigh
			report.Note = "the vendor reports the spend limit as reached"
		}
		return report
	}
	spend := in.SpendUSD
	if in.RateFactor > 0 && in.RateFactor != 1 {
		spend *= in.RateFactor
	}
	report.SpendUSD, report.SpendLimitUSD, report.SpendSource = spend, in.SpendLimitUSD, "estimate"
	if in.SpendLimitUSD <= 0 {
		report.Note = "no spend limit known, so there is no denominator — set up the Claude usage meter " +
			"(`tokenops vendor-usage setup claude-usage-meter`) to read it from Anthropic, or set " +
			"plan_limits.<provider>.spend_limit_usd to the figure your admins configured"
		return report
	}
	report.SpendPct = math.Round(spend/in.SpendLimitUSD*10000) / 100
	report.OverageRisk = classifyWindowRisk(report.SpendPct)
	return report
}

// estimatedResetIn is how long until a rolling window resets when the vendor
// has not said: the window opens with its first message, so it resets one
// window length after that. ok is false when nothing has happened inside the
// window — no window is running, so there is no reset to report.
//
// This replaces "now + window length", which put the reset a full window
// away on every call — "resets in 5h0m0s" whether the window opened a
// minute or four hours ago.
func estimatedResetIn(started time.Time, window time.Duration, now time.Time) (time.Duration, bool) {
	if started.IsZero() || window <= 0 {
		return 0, false
	}
	d := started.Add(window).Sub(now)
	if d <= 0 {
		return 0, false
	}
	return d, true
}

func setEstimatedReset(r *HeadroomReport, started time.Time, window time.Duration, now time.Time) {
	d, ok := estimatedResetIn(started, window, now)
	if !ok {
		return
	}
	r.WindowResetsAt = now.Add(d).UTC()
	r.WindowResetsIn = d.Round(time.Minute).String()
	r.WindowResetEstimated = true
}
