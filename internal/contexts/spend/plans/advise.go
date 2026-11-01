package plans

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// Plan right-sizing replays the windows a plan's meter reported over a
// period against every tier of the plan's family, after the savings-plan
// recommenders of the cloud vendors: what each tier would have cost, how
// often it would have run out and for how long, and how full its windows
// would have got. It is advice. Nothing here binds or switches a plan.
//
// A tier's allowance is a multiple of its family's base plan (Max is five
// times Pro). A weekly reading of 40% on Ultra is 4 Pro weeks of demand,
// which is 80% of Max: the replay scales each measured share by the ratio
// of the multiples, so it never needs the base plan's absolute allowance.
// A tier may state its weekly multiple apart from its per-session one, or
// have no session limit at all (Ultra); where the vendor states one
// multiple, both windows are assumed to scale by it, which is why the
// answer carries a confidence label.

// Option kinds.
const (
	// OptionPlan is a tier on its own: running out means waiting.
	OptionPlan = "plan"
	// OptionPlanOverflow is a tier whose overflow is billed at API list
	// price instead of locking out (Anthropic's extra usage).
	OptionPlanOverflow = "plan_overflow"
	// OptionAPI is no subscription: every request at API list price.
	OptionAPI = "api"
)

// Verdicts.
const (
	VerdictStay    = "stay"
	VerdictSwitch  = "switch"
	VerdictUpgrade = "upgrade"
)

const (
	// AdviceDays is the default period replayed.
	AdviceDays = 30
	// adviceMinDays is the least history a confident answer needs: two
	// full weekly windows.
	adviceMinDays = 14
	// adviceMaterialUSD and adviceMaterialShare are the smallest monthly
	// saving worth a switch: both must be met.
	adviceMaterialUSD   = 10.0
	adviceMaterialShare = 0.10
	// adviceFewLockouts and adviceFewBlocked are the most running out per
	// month an option may have and still be recommended.
	adviceFewLockouts = 1.0
	adviceFewBlocked  = 2 * time.Hour
)

// Tier is a plan as a replay candidate.
type Tier struct {
	Name    string `json:"plan"`
	Display string `json:"display"`
	// Factor is the tier's weekly allowance as a multiple of its family's
	// base; SessionFactor its 5-hour one, absent when NoSessionLimit.
	Factor         float64 `json:"factor"`
	SessionFactor  float64 `json:"session_factor,omitempty"`
	NoSessionLimit bool    `json:"no_session_limit,omitempty"`
	// Derived is true when the allowance is stated as a multiple of
	// another plan rather than published for the tier itself.
	Derived bool `json:"derived,omitempty"`
	// MonthlyUSD is the tier's monthly fee; zero when unknown.
	MonthlyUSD float64 `json:"monthly_usd,omitempty"`
	// PriceSource is "yours" (what the operator said they pay) or "list".
	PriceSource string `json:"price_source,omitempty"`
}

// sessionWindowMax is the longest window that is a session (5-hour)
// window rather than a weekly one.
const sessionWindowMax = 24 * time.Hour

// TierOf is the family a plan belongs to, named by its base plan, and the
// plan's weekly allowance as a multiple of the base. ok is false for a
// plan with no stated multiple, or one billed at API rates, which has no
// window to scale.
func TierOf(name string) (base string, factor float64, ok bool) {
	t, base, ok := tierOf(name)
	return base, t.Factor, ok
}

// tierOf is the plan as a replay candidate, unpriced, with its family.
func tierOf(name string) (Tier, string, bool) {
	p, known := Lookup(name)
	if !known || p.SpendDenominated {
		return Tier{}, "", false
	}
	t := Tier{Name: p.Name, Display: p.Display, NoSessionLimit: p.NoSessionLimit}
	if weekly := weeklyMultiplier(p); p.RelativeTo != "" && weekly > 0 {
		t.Factor, t.Derived = weekly, true
		if !p.NoSessionLimit {
			t.SessionFactor = p.Multiplier
		}
		return t, p.RelativeTo, true
	}
	for _, q := range catalog {
		if q.RelativeTo == p.Name && weeklyMultiplier(q) > 0 {
			t.Factor, t.SessionFactor = 1, 1
			return t, p.Name, true
		}
	}
	return Tier{}, "", false
}

// weeklyMultiplier is p's weekly allowance as a multiple of its base's.
func weeklyMultiplier(p Plan) float64 {
	if p.WeeklyMultiplier > 0 {
		return p.WeeklyMultiplier
	}
	return p.Multiplier
}

// WindowFactor is the plan's allowance in a window of length d, as a
// multiple of its family's base, with the base. ok is false for a plan
// with no stated multiple, and for a session window on a tier that has no
// session limit.
func WindowFactor(name string, d time.Duration) (base string, factor float64, ok bool) {
	t, base, ok := tierOf(name)
	if !ok {
		return "", 0, false
	}
	f, limited := t.Allowance(d)
	return base, f, limited
}

// Allowance is the tier's allowance in a window of length d, as a multiple
// of its family's base. limited is false when the tier has no limit in
// such a window: a session window on a tier with no session limit.
func (t Tier) Allowance(d time.Duration) (factor float64, limited bool) {
	if d > sessionWindowMax {
		return t.Factor, t.Factor > 0
	}
	if t.NoSessionLimit {
		return 0, false
	}
	if t.SessionFactor > 0 {
		return t.SessionFactor, true
	}
	return t.Factor, t.Factor > 0
}

// ObservedTiers gives each tier the allowances the overlay observed for
// it, in place of the catalog's, and returns the limits it used.
func ObservedTiers(tiers []Tier, o ObservedLimits) []ObservedLimit {
	var used []ObservedLimit
	for i := range tiers {
		if l, ok := o.Factor(tiers[i].Name, week); ok {
			tiers[i].Factor = l.ObservedFactor
			used = append(used, l)
		}
		if tiers[i].NoSessionLimit {
			continue
		}
		if l, ok := o.Factor(tiers[i].Name, 5*time.Hour); ok {
			tiers[i].SessionFactor = l.ObservedFactor
			used = append(used, l)
		}
	}
	return used
}

// TierFamily is every tier one person can move between from plan, by
// allowance, at list price. Per-seat plans are bought for a team with a
// minimum of seats, so they are left out unless plan is one.
func TierFamily(plan string) []Tier {
	base, _, ok := TierOf(plan)
	if !ok {
		return nil
	}
	current, _ := Lookup(plan)
	var out []Tier
	for _, name := range Names() {
		t, b, ok := tierOf(name)
		p := catalog[name]
		if !ok || b != base || (p.PerSeat && name != current.Name) {
			continue
		}
		t.MonthlyUSD = p.MonthlyUSD
		if t.MonthlyUSD > 0 {
			t.PriceSource = "list"
		}
		out = append(out, t)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Factor < out[j].Factor })
	return out
}

// Option is one way the period could have been paid for.
type Option struct {
	Kind    string `json:"kind"`
	Plan    string `json:"plan,omitempty"`
	Display string `json:"display"`
	// Current marks the plan in force now.
	Current bool `json:"current,omitempty"`
	// Lockouts is how many windows would have run out; BlockedMinutes is
	// how long, from running out to each window's reset, with overlapping
	// stretches counted once.
	Lockouts       int `json:"lockouts"`
	BlockedMinutes int `json:"blocked_minutes"`
	// PeakPct is the fullest any window would have got, in PeakWindow.
	// Above 100 on an overflow option is the share billed per token.
	PeakPct    *float64 `json:"peak_pct,omitempty"`
	PeakWindow string   `json:"peak_window,omitempty"`
	// FeeUSD is the monthly fee; MeteredUSD the monthly usage billed at
	// API list price (the overflow, or everything for the API option).
	FeeUSD     float64  `json:"fee_usd"`
	MeteredUSD *float64 `json:"metered_usd,omitempty"`
	// MonthlyUSD is the total per month; nil when it cannot be priced.
	MonthlyUSD *float64 `json:"monthly_usd,omitempty"`
	// SavingsUSD is what it would have saved per month against the plan
	// in force; negative costs more.
	SavingsUSD  *float64 `json:"savings_usd,omitempty"`
	PriceSource string   `json:"price_source,omitempty"`
}

// Recommendation is the option the replay favours.
type Recommendation struct {
	Verdict string `json:"verdict"`
	Kind    string `json:"kind"`
	Plan    string `json:"plan,omitempty"`
	Display string `json:"display"`
	// SavingsUSD is per month against the plan in force; negative for an
	// upgrade.
	SavingsUSD float64 `json:"savings_usd"`
	// Material is true when the saving is worth a switch: at least $10
	// and a tenth of what the plan in force costs.
	Material bool   `json:"material,omitempty"`
	Summary  string `json:"summary"`
}

// Advice is one provider's replay.
type Advice struct {
	Provider string    `json:"provider"`
	Plan     string    `json:"plan"`
	Display  string    `json:"display"`
	Since    time.Time `json:"since"`
	Until    time.Time `json:"until"`
	// Days is how much of the period the readings cover.
	Days float64 `json:"days"`
	// WindowsMeasured counts the window instances replayed.
	WindowsMeasured int `json:"windows_measured"`
	// ListValueUSD is the plan-covered usage at API list price over the
	// period; nil when there is no rate card to price it.
	ListValueUSD   *float64        `json:"list_value_usd,omitempty"`
	Options        []Option        `json:"options,omitempty"`
	Recommendation *Recommendation `json:"recommendation,omitempty"`
	Confidence     string          `json:"confidence"`
	Caveats        []string        `json:"caveats,omitempty"`
	// EarlyResets is what the vendor's early resets did to the current
	// plan's lockouts; nil when none ended one.
	EarlyResets *EarlyResetCut `json:"early_resets,omitempty"`
	// Observed are the limits the replay took from the local overlay of
	// observed limits instead of the catalog, each with both figures; nil
	// when it used the catalog throughout.
	Observed []ObservedLimit `json:"observed,omitempty"`
}

// EarlyResetCut is the lockouts a vendor's early resets ended, and the
// blocked time they ended at: from running out to the early reset, not to
// the window's scheduled reset.
type EarlyResetCut struct {
	Count          int    `json:"count"`
	BlockedMinutes int    `json:"blocked_minutes"`
	Vendor         string `json:"vendor"`
	// Window names the window in words ("weekly", "5-hour"); "" when the
	// early resets were on windows of different lengths.
	Window string `json:"window,omitempty"`
}

// WorthSaying is true when the advice belongs on an ambient surface: a
// cheaper option that saves materially with no more than a lockout a
// month, read with at least medium confidence.
func (a Advice) WorthSaying() bool {
	r := a.Recommendation
	return r != nil && r.Verdict == VerdictSwitch && r.Material && a.Confidence != ConfidenceLow
}

// ReplayInput is what a replay reads.
type ReplayInput struct {
	Provider     string
	Since, Until time.Time
	// Readings are the meter's window readings over the period.
	Readings []QuotaReading
	// FactorAt is the allowance of the plan in force at a moment in a
	// window of the given length, so a reading taken before a switch is
	// scaled by the plan it was taken on. ok is false when that plan had no
	// limit in such a window, or is not of the family.
	FactorAt func(at time.Time, window time.Duration) (float64, bool)
	// Current is the plan in force now, priced; Candidates its family.
	Current    Tier
	Candidates []Tier
	// ListValueUSD is the plan-covered usage at API list price over the
	// period, when ListValueKnown. UnpricedRequests counts requests with
	// no rate, which the value leaves out.
	ListValueUSD     float64
	ListValueKnown   bool
	UnpricedRequests int
	// EarlyResets are the vendor's early resets of this provider's windows
	// in the period (EarlyResetsIn). A lockout one ended is blocked only
	// until it, not until the window's scheduled reset.
	EarlyResets []EarlyReset
	// Observed are the observed limits FactorAt and the candidates were
	// given in place of the catalog's (ObservedTiers); the advice names
	// them.
	Observed []ObservedLimit
}

// QuotaReadingsFrom is every window reading of provider's plan in
// [since, until], oldest first.
func QuotaReadingsFrom(envs []*eventschema.Envelope, provider string, since, until time.Time) []QuotaReading {
	var out []QuotaReading
	for _, e := range envs {
		if e == nil || e.Attributes == nil || e.Timestamp.Before(since) || e.Timestamp.After(until) {
			continue
		}
		p, ok := windowSourceProvider[e.Source]
		if !ok || string(p) != provider {
			continue
		}
		if ws := QuotaWindowsFromAttributes(p, e.Attributes); len(ws) > 0 {
			out = append(out, QuotaReading{At: e.Timestamp, Windows: ws})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].At.Before(out[j].At) })
	return out
}

// ListValue is provider's plan-covered usage in [since, until] at API list
// price, and how many requests had no rate.
func ListValue(envs []*eventschema.Envelope, provider string, since, until time.Time, price Pricer) (usd float64, unpriced int) {
	for _, e := range envs {
		if e == nil || e.Timestamp.Before(since) || e.Timestamp.After(until) || !perRequest(e) {
			continue
		}
		p, ok := e.Payload.(*eventschema.PromptEvent)
		if !ok || string(p.Provider) != provider || p.CostSource != eventschema.CostSourcePlanIncluded {
			continue
		}
		billed := *p
		billed.CostSource = eventschema.CostSourceMetered
		c, err := price(&billed, e.Timestamp)
		if err != nil {
			unpriced++
			continue
		}
		usd += c
	}
	return usd, unpriced
}

// windowPoint is one reading of a window instance, as demand in base-plan
// windows.
type windowPoint struct {
	at     time.Time
	demand float64
}

// windowInstance is one window of one kind, from its start to its reset.
type windowInstance struct {
	label    string
	duration time.Duration
	reset    time.Time
	points   []windowPoint
	peak     float64
}

// instancesOf groups readings into window instances, scaling each share
// by the plan in force when it was read.
func instancesOf(rs []QuotaReading, factorAt func(time.Time, time.Duration) (float64, bool)) []windowInstance {
	type key struct {
		label string
		reset time.Time
	}
	byLabel := map[string][]windowPoint{}
	resets := map[string][]time.Time{}
	durations := map[string]time.Duration{}
	for _, r := range rs {
		for _, w := range r.Windows {
			if w.Label == "" || w.ResetsAt.IsZero() || w.Duration <= 0 || strings.Contains(w.Label, "spend limit") {
				continue
			}
			f, ok := factorAt(r.At, w.Duration)
			if !ok || f <= 0 {
				continue
			}
			byLabel[w.Label] = append(byLabel[w.Label], windowPoint{at: r.At, demand: w.UsedPct / 100 * f})
			resets[w.Label] = append(resets[w.Label], w.ResetsAt)
			durations[w.Label] = w.Duration
		}
	}
	out := make([]windowInstance, 0, len(rs))
	for label, pts := range byLabel {
		rst := resets[label]
		idx := make([]int, len(pts))
		for i := range idx {
			idx[i] = i
		}
		sort.SliceStable(idx, func(a, b int) bool { return rst[idx[a]].Before(rst[idx[b]]) })
		var cur *windowInstance
		groups := map[key]*windowInstance{}
		var order []key
		for _, i := range idx {
			if cur == nil || rst[i].Sub(cur.reset) >= instanceResetGranularity {
				k := key{label, rst[i]}
				cur = &windowInstance{label: label, duration: durations[label], reset: rst[i]}
				groups[k] = cur
				order = append(order, k)
			}
			cur.points = append(cur.points, pts[i])
		}
		for _, k := range order {
			in := groups[k]
			sort.SliceStable(in.points, func(a, b int) bool { return in.points[a].at.Before(in.points[b].at) })
			for _, p := range in.points {
				in.peak = math.Max(in.peak, p.demand)
			}
			out = append(out, *in)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if !out[i].reset.Equal(out[j].reset) {
			return out[i].reset.Before(out[j].reset)
		}
		return out[i].label < out[j].label
	})
	return out
}

// exhaustedAt is when the instance's demand first reached factor,
// interpolated between the readings either side.
func (w windowInstance) exhaustedAt(factor float64) (time.Time, bool) {
	const eps = 1e-9
	for i, p := range w.points {
		if p.demand+eps < factor {
			continue
		}
		if i == 0 {
			return p.at, true
		}
		prev := w.points[i-1]
		frac := (factor - prev.demand) / (p.demand - prev.demand)
		return prev.at.Add(time.Duration(frac * float64(p.at.Sub(prev.at)))), true
	}
	return time.Time{}, false
}

// simulation is one tier's replay.
type simulation struct {
	lockouts   int
	blocked    time.Duration
	peakPct    float64
	peakWindow string
	// cut are the early resets that ended a lockout, and cutBlocked the
	// blocked time up to them.
	cut        []EarlyReset
	cutBlocked time.Duration
	// overflowShare is the share of the period's demand above the tier's
	// allowance, in the window kind where it is largest.
	overflowShare float64
}

func simulate(ws []windowInstance, t Tier, until time.Time, early []EarlyReset) simulation {
	var s simulation
	var spans, cutSpans [][2]time.Time
	total, over := map[string]float64{}, map[string]float64{}
	for _, w := range ws {
		factor, limited := t.Allowance(w.duration)
		if !limited {
			continue
		}
		pct := math.Round(w.peak/factor*1000) / 10
		if pct > s.peakPct || (pct == s.peakPct && w.label > s.peakWindow) {
			s.peakPct, s.peakWindow = pct, w.label
		}
		total[w.label] += w.peak
		over[w.label] += math.Max(0, w.peak-factor)
		at, out := w.exhaustedAt(factor)
		if !out {
			continue
		}
		s.lockouts++
		end, by := w.reset, EarlyReset{}
		if e, ok := earlyResetEnding(w, at, early); ok {
			end, by = e.ObservedAt, e
			s.cut = append(s.cut, e)
		}
		if end.After(until) {
			end = until
		}
		if end.After(at) {
			spans = append(spans, [2]time.Time{at, end})
			if !by.ObservedAt.IsZero() {
				cutSpans = append(cutSpans, [2]time.Time{at, end})
			}
		}
	}
	s.cutBlocked = unionLength(cutSpans)
	for label, t := range total {
		if t > 0 {
			s.overflowShare = math.Max(s.overflowShare, over[label]/t)
		}
	}
	s.blocked = unionLength(spans)
	return s
}

// earlyResetEnding is the early reset that ended the instance's lockout,
// the one that reset the window after it ran out at at. The instance's
// scheduled reset is the one the early reset replaced.
func earlyResetEnding(w windowInstance, at time.Time, early []EarlyReset) (EarlyReset, bool) {
	for _, e := range early {
		if e.PrevResetsAt.Sub(w.reset).Abs() <= instanceResetGranularity && e.ObservedAt.After(at) && e.ObservedAt.Before(w.reset) {
			return e, true
		}
	}
	return EarlyReset{}, false
}

// unionLength is how long the spans cover together.
func unionLength(spans [][2]time.Time) time.Duration {
	var total time.Duration
	for _, s := range unionSpans(spans) {
		total += s[1].Sub(s[0])
	}
	return total
}

// Replay replays the period against every candidate tier, the tiers with
// metered overflow, and the API at list price, and recommends one.
func Replay(in ReplayInput) Advice {
	a := Advice{Provider: in.Provider, Plan: in.Current.Name, Display: in.Current.Display,
		Since: in.Since, Until: in.Until, Confidence: ConfidenceLow, Observed: in.Observed}
	factorAt := in.FactorAt
	if factorAt == nil {
		factorAt = func(_ time.Time, d time.Duration) (float64, bool) { return in.Current.Allowance(d) }
	}
	ws := instancesOf(in.Readings, factorAt)
	a.WindowsMeasured = len(ws)
	if len(ws) == 0 {
		a.Caveats = append(a.Caveats, "No plan window readings in the period, so there is nothing to replay; "+
			"connect a meter that reports the windows (`tokenops vendor-usage setup claude-subscription`)")
		return a
	}
	first := in.Until
	for _, w := range ws {
		if t := w.points[0].at; t.Before(first) {
			first = t
		}
	}
	if first.Before(in.Since) {
		first = in.Since
	}
	a.Days = math.Round(in.Until.Sub(first).Hours()/24*10) / 10
	if a.Days <= 0 {
		a.Days = 0.1
	}
	perMonth := averageMonthDays / a.Days
	if in.ListValueKnown {
		v := round2(in.ListValueUSD)
		a.ListValueUSD = &v
	}

	candidates := in.Candidates
	if !hasTier(candidates, in.Current.Name) {
		candidates = append(candidates, in.Current)
	}
	sims := map[string]simulation{}
	for _, t := range candidates {
		s := simulate(ws, t, in.Until, in.EarlyResets)
		sims[t.Name] = s
		o := Option{Kind: OptionPlan, Plan: t.Name, Display: t.Display, Current: t.Name == in.Current.Name,
			Lockouts: s.lockouts, BlockedMinutes: int(s.blocked.Round(time.Minute) / time.Minute),
			FeeUSD: t.MonthlyUSD, PriceSource: t.PriceSource}
		o.PeakPct, o.PeakWindow = ptrFloat(s.peakPct), s.peakWindow
		if t.MonthlyUSD > 0 {
			o.MonthlyUSD = ptrFloat(round2(t.MonthlyUSD))
		}
		a.Options = append(a.Options, o)
	}
	if in.ListValueKnown {
		for _, t := range candidates {
			s := sims[t.Name]
			if s.lockouts == 0 || t.MonthlyUSD <= 0 {
				continue
			}
			metered := round2(s.overflowShare * in.ListValueUSD * perMonth)
			o := Option{Kind: OptionPlanOverflow, Plan: t.Name, Display: t.Display + " + metered overflow",
				FeeUSD: t.MonthlyUSD, MeteredUSD: &metered, PriceSource: t.PriceSource,
				MonthlyUSD: ptrFloat(round2(t.MonthlyUSD + metered))}
			o.PeakPct, o.PeakWindow = ptrFloat(s.peakPct), s.peakWindow
			a.Options = append(a.Options, o)
		}
		metered := round2(in.ListValueUSD * perMonth)
		a.Options = append(a.Options, Option{Kind: OptionAPI, Display: "API at list price (no subscription)",
			MeteredUSD: &metered, MonthlyUSD: ptrFloat(metered), PriceSource: "list"})
	}
	cur := currentOption(a.Options)
	if cur != nil && cur.MonthlyUSD != nil {
		for i := range a.Options {
			if m := a.Options[i].MonthlyUSD; m != nil {
				a.Options[i].SavingsUSD = ptrFloat(round2(*cur.MonthlyUSD - *m))
			}
		}
	}
	a.Confidence = confidence(in, a.Days, ws)
	a.Caveats = caveats(in, cur, candidates)
	for _, l := range in.Observed {
		a.Caveats = append(a.Caveats, "Uses the limit your own windows show, not the catalog's: "+l.Words()+
			"; `tokenops plan observed reset` goes back to the catalog")
	}
	if cur != nil && len(sims[cur.Plan].cut) > 0 {
		s := sims[cur.Plan]
		a.EarlyResets = &EarlyResetCut{Count: len(s.cut), BlockedMinutes: int(s.cutBlocked.Round(time.Minute) / time.Minute),
			Vendor: vendorName(in.Provider), Window: windowWord(s.cut)}
	}
	a.Recommendation = recommend(a.Options, cur, perMonth, a.EarlyResets)
	return a
}

func hasTier(ts []Tier, name string) bool {
	for _, t := range ts {
		if t.Name == name {
			return true
		}
	}
	return false
}

func currentOption(os []Option) *Option {
	for i := range os {
		if os[i].Current && os[i].Kind == OptionPlan {
			return &os[i]
		}
	}
	return nil
}

func confidence(in ReplayInput, days float64, ws []windowInstance) string {
	longest := map[string]int{}
	for _, w := range ws {
		longest[w.label]++
	}
	weekly := longest["weekly"]
	if days < adviceMinDays || (weekly > 0 && weekly < 2) || sessionsUnseen(in) {
		return ConfidenceLow
	}
	for _, t := range in.Candidates {
		if t.Derived {
			return ConfidenceMedium
		}
	}
	if in.Current.Derived {
		return ConfidenceMedium
	}
	return ConfidenceHigh
}

// sessionsUnseen is true when the plan in force has no session limit and
// a candidate has one: the replay sees none of the demand a session window
// would have run out on.
func sessionsUnseen(in ReplayInput) bool {
	if !in.Current.NoSessionLimit {
		return false
	}
	for _, t := range in.Candidates {
		if !t.NoSessionLimit {
			return true
		}
	}
	return false
}

func caveats(in ReplayInput, cur *Option, candidates []Tier) []string {
	var out []string
	var derived []string
	base := ""
	for _, t := range candidates {
		if t.Derived {
			derived = append(derived, t.Display)
		} else {
			base = t.Display
		}
	}
	if len(derived) > 0 {
		if base == "" {
			base = "the base plan"
		}
		out = append(out, fmt.Sprintf("Tier allowances are derived: %s are the vendor's stated multiples of %s's allowance, "+
			"and %s's own allowance is unverified (the vendor no longer publishes it). The replay scales your measured "+
			"window shares by those multiples; where a tier states one multiple, its weekly windows are assumed to scale by it too.",
			joinAnd(derived), base, base))
	}
	if sessionsUnseen(in) {
		out = append(out, fmt.Sprintf("%s has no 5-hour limit, so its readings say nothing of 5-hour demand: "+
			"how often a tier with one would have run out within 5 hours is not known, and its figures cover the weekly windows only.",
			in.Current.Display))
	}
	if cur != nil && cur.Lockouts > 0 {
		out = append(out, fmt.Sprintf("You ran out %d time(s) on %s: work you could not do then left no readings, "+
			"so the larger tiers' figures are a floor.", cur.Lockouts, cur.Display))
	}
	if in.ListValueKnown {
		out = append(out, "Overflow and API costs price the plan-covered usage at API list price; "+
			"usage already billed per token is the same under every option and is left out.")
		if in.UnpricedRequests > 0 {
			out = append(out, fmt.Sprintf("%d request(s) used models with no API rate, so the metered figures understate them.", in.UnpricedRequests))
		}
	} else {
		out = append(out, "No rate card to price the usage at API rates, so the overflow and API options are left out.")
	}
	return out
}

// acceptable is an option that runs out at most a few times a month.
func acceptable(o Option, perMonth float64) bool {
	return float64(o.Lockouts)*perMonth <= adviceFewLockouts &&
		time.Duration(float64(o.BlockedMinutes)*perMonth)*time.Minute <= adviceFewBlocked
}

var kindRank = map[string]int{OptionPlan: 0, OptionPlanOverflow: 1, OptionAPI: 2}

func recommend(os []Option, cur *Option, perMonth float64, cut *EarlyResetCut) *Recommendation {
	if cur == nil || cur.MonthlyUSD == nil {
		return nil
	}
	var best *Option
	better := func(o, b *Option) bool {
		if *o.MonthlyUSD != *b.MonthlyUSD {
			return *o.MonthlyUSD < *b.MonthlyUSD
		}
		if o.Current != b.Current {
			return o.Current
		}
		return kindRank[o.Kind] < kindRank[b.Kind]
	}
	for i := range os {
		o := &os[i]
		if o.MonthlyUSD == nil || !acceptable(*o, perMonth) {
			continue
		}
		if best == nil || better(o, best) {
			best = o
		}
	}
	if best == nil {
		// Nothing priced avoids running out: the plan that runs out least.
		for i := range os {
			o := &os[i]
			if o.MonthlyUSD == nil || o.Kind != OptionPlan {
				continue
			}
			if best == nil || o.BlockedMinutes < best.BlockedMinutes ||
				(o.BlockedMinutes == best.BlockedMinutes && better(o, best)) {
				best = o
			}
		}
	}
	if best == nil {
		return nil
	}
	saving := round2(*cur.MonthlyUSD - *best.MonthlyUSD)
	material := saving >= adviceMaterialUSD && saving >= adviceMaterialShare**cur.MonthlyUSD
	r := &Recommendation{Kind: best.Kind, Plan: best.Plan, Display: best.Display, SavingsUSD: saving, Material: material}
	switch {
	case best.Current:
		r.Verdict = VerdictStay
		if !acceptable(*cur, perMonth) && !hasLargerPlan(os, cur) {
			// The top tier runs out too often and nothing larger exists:
			// "fits" would be wrong, so say why it stays.
			r.Summary = topTierSummary(cur, cut)
			return r
		}
	case saving > 0 && (material || !acceptable(*cur, perMonth)):
		// Cheaper, and either worth the switch or running out less.
		r.Verdict = VerdictSwitch
	case acceptable(*cur, perMonth):
		r.Verdict, r.Kind, r.Plan, r.Display, r.SavingsUSD, r.Material = VerdictStay, cur.Kind, cur.Plan, cur.Display, 0, false
		best = cur
	default:
		// The plan in force runs out and nothing cheaper fits. Upgrade
		// only to a larger allowance; with none to move to, stay and say so.
		up := cheapestHigherAcceptable(os, cur, perMonth)
		if up == nil {
			r.Verdict, r.Kind, r.Plan, r.Display, r.SavingsUSD, r.Material = VerdictStay, cur.Kind, cur.Plan, cur.Display, 0, false
			r.Summary = topTierSummary(cur, cut)
			return r
		}
		best = up
		r.Verdict, r.Kind, r.Plan, r.Display = VerdictUpgrade, up.Kind, up.Plan, up.Display
		r.SavingsUSD, r.Material = round2(*cur.MonthlyUSD-*up.MonthlyUSD), false
	}
	r.Summary = summarize(r, best, cur)
	return r
}

// cheapestHigherAcceptable is the cheapest priced option that runs out
// rarely enough and sits on a tier with a larger allowance than cur's, or
// nil when there is none. Overflow options carry no lockouts of their own,
// so they qualify on the tier they overflow.
func cheapestHigherAcceptable(os []Option, cur *Option, perMonth float64) *Option {
	curFactor := planFactor(cur.Plan)
	var best *Option
	for i := range os {
		o := &os[i]
		if o.MonthlyUSD == nil || o.Kind == OptionAPI || planFactor(o.Plan) <= curFactor || !acceptable(*o, perMonth) {
			continue
		}
		if best == nil || *o.MonthlyUSD < *best.MonthlyUSD {
			best = o
		}
	}
	return best
}

// hasLargerPlan is true when a plan with a larger allowance than cur's is
// among the options.
func hasLargerPlan(os []Option, cur *Option) bool {
	curFactor := planFactor(cur.Plan)
	for i := range os {
		if os[i].Kind == OptionPlan && planFactor(os[i].Plan) > curFactor {
			return true
		}
	}
	return false
}

// planFactor is a plan's allowance as a multiple of its family's base; zero
// when the plan has none.
func planFactor(name string) float64 {
	_, f, _ := TierOf(name)
	return f
}

func summarize(r *Recommendation, best, cur *Option) string {
	peak := func(o *Option) string {
		if o.PeakPct == nil || o.PeakWindow == "" {
			return ""
		}
		return fmt.Sprintf("%.0f%% of the %s window", *o.PeakPct, o.PeakWindow)
	}
	switch r.Verdict {
	case VerdictSwitch:
		s := fmt.Sprintf("%s would have cost $%.0f/mo less than %s", best.Display, r.SavingsUSD, cur.Display)
		if best.Kind == OptionPlanOverflow && best.MeteredUSD != nil {
			s += fmt.Sprintf(" ($%.0f/mo of it metered overflow)", *best.MeteredUSD)
		}
		s += lockoutWords(best)
		if p := peak(cur); p != "" {
			s += fmt.Sprintf("; on %s you peaked at %s", cur.Display, p)
		}
		return s + "."
	case VerdictUpgrade:
		return fmt.Sprintf("%s ran out %d time(s) (about %s blocked); %s would have run out %d time(s) for $%.0f/mo more.",
			cur.Display, cur.Lockouts, hoursWords(cur.BlockedMinutes), best.Display, best.Lockouts, -r.SavingsUSD)
	}
	s := fmt.Sprintf("%s fits", cur.Display)
	if p := peak(cur); p != "" {
		s += fmt.Sprintf(": you peaked at %s", p)
	}
	return s + "; no cheaper option would have saved materially without running out."
}

func lockoutWords(o *Option) string {
	if o.Lockouts == 0 {
		return ", with no lockouts"
	}
	return fmt.Sprintf(", running out %d time(s) (about %s)", o.Lockouts, hoursWords(o.BlockedMinutes))
}

func hoursWords(minutes int) string {
	if minutes < 60 {
		return fmt.Sprintf("%dm", minutes)
	}
	return fmt.Sprintf("%.1fh", float64(minutes)/60)
}

// joinAnd lists names for a sentence: "a", "a and b", "a, b and c".
func joinAnd(names []string) string {
	if len(names) < 2 {
		return strings.Join(names, "")
	}
	return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
}

// topTierSummary says why the top tier stays. When the vendor's early
// resets ended some of its lockouts, it says so and how much blocked time
// they cut out.
func topTierSummary(cur *Option, cut *EarlyResetCut) string {
	if cut == nil || cut.Count == 0 {
		return fmt.Sprintf("%s ran out %d time(s) (about %s blocked) and is the top tier: no larger plan to move to, "+
			"so it stays; the lockouts are the price of this tier's allowance.",
			cur.Display, cur.Lockouts, hoursWords(cur.BlockedMinutes))
	}
	window := "the window"
	if cut.Window != "" {
		window = "the " + cut.Window + " window"
	}
	when := fmt.Sprintf("%d of those times", cut.Count)
	if cut.Count == cur.Lockouts {
		when = map[int]string{1: "that time", 2: "both times"}[cut.Count]
		if cut.Count > 2 {
			when = "each time"
		}
	}
	return fmt.Sprintf("%s is the top tier; it ran out %s, but %s reset %s early %s, so about %s were lost. "+
		"No larger plan to move to, so it stays.",
		cur.Display, lockoutCount(cur.Lockouts), cut.Vendor, window, when, hoursWords(cut.BlockedMinutes))
}

// lockoutCount is a count of lockouts in words for a sentence.
func lockoutCount(n int) string {
	switch n {
	case 1:
		return "once"
	case 2:
		return "twice"
	}
	return fmt.Sprintf("%d times", n)
}

// windowWord names the windows the early resets were on: "weekly" or
// "5-hour" when they all share one length, "" when they differ.
func windowWord(early []EarlyReset) string {
	word := ""
	for _, e := range early {
		w := ""
		switch e.Duration {
		case week:
			w = "weekly"
		case 5 * time.Hour:
			w = "5-hour"
		}
		if word != "" && w != word {
			return ""
		}
		word = w
	}
	return word
}

// vendorName is the vendor's name for a provider id, as it reads in a
// sentence.
func vendorName(provider string) string {
	names := map[string]string{"openai": "OpenAI", "anthropic": "Anthropic"}
	if n, ok := names[provider]; ok {
		return n
	}
	return provider
}

func ptrFloat(v float64) *float64 { return &v }

func round2(v float64) float64 { return math.Round(v*100) / 100 }
