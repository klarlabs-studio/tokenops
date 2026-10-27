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
// A tier's allowance is a multiple of its family's base plan (Max 5x is
// five times Pro). A reading of 40% on Max 20x is 8 Pro windows of demand,
// which is 160% of Max 5x: the replay scales each measured share by the
// ratio of the multiples, so it never needs the base plan's absolute
// allowance. The multiples are what the vendor states; whether every
// window (the weekly ones too) scales by them is not published, which is
// why the answer carries a confidence label.

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
	// Factor is the tier's allowance as a multiple of its family's base.
	Factor float64 `json:"factor"`
	// Derived is true when the allowance is stated as a multiple of
	// another plan rather than published for the tier itself.
	Derived bool `json:"derived,omitempty"`
	// MonthlyUSD is the tier's monthly fee; zero when unknown.
	MonthlyUSD float64 `json:"monthly_usd,omitempty"`
	// PriceSource is "yours" (what the operator said they pay) or "list".
	PriceSource string `json:"price_source,omitempty"`
}

// TierOf is the family a plan belongs to, named by its base plan, and the
// plan's allowance as a multiple of the base. ok is false for a plan with
// no stated multiple, or one billed at API rates, which has no window to
// scale.
func TierOf(name string) (base string, factor float64, ok bool) {
	p, known := Lookup(name)
	if !known || p.SpendDenominated {
		return "", 0, false
	}
	if p.RelativeTo != "" && p.Multiplier > 0 {
		return p.RelativeTo, p.Multiplier, true
	}
	for _, q := range catalog {
		if q.RelativeTo == p.Name && q.Multiplier > 0 {
			return p.Name, 1, true
		}
	}
	return "", 0, false
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
		b, factor, ok := TierOf(name)
		p := catalog[name]
		if !ok || b != base || (p.PerSeat && name != current.Name) {
			continue
		}
		t := Tier{Name: name, Display: p.Display, Factor: factor, Derived: p.RelativeTo != "", MonthlyUSD: p.MonthlyUSD}
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
	// FactorAt is the multiple of the plan in force at a moment, so a
	// reading taken before a switch is scaled by the plan it was taken on.
	FactorAt func(time.Time) (float64, bool)
	// Current is the plan in force now, priced; Candidates its family.
	Current    Tier
	Candidates []Tier
	// ListValueUSD is the plan-covered usage at API list price over the
	// period, when ListValueKnown. UnpricedRequests counts requests with
	// no rate, which the value leaves out.
	ListValueUSD     float64
	ListValueKnown   bool
	UnpricedRequests int
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
	label  string
	reset  time.Time
	points []windowPoint
	peak   float64
}

// instancesOf groups readings into window instances, scaling each share
// by the plan in force when it was read.
func instancesOf(rs []QuotaReading, factorAt func(time.Time) (float64, bool)) []windowInstance {
	type key struct {
		label string
		reset time.Time
	}
	byLabel := map[string][]windowPoint{}
	resets := map[string][]time.Time{}
	for _, r := range rs {
		f, ok := factorAt(r.At)
		if !ok || f <= 0 {
			continue
		}
		for _, w := range r.Windows {
			if w.Label == "" || w.ResetsAt.IsZero() || w.Duration <= 0 || strings.Contains(w.Label, "spend limit") {
				continue
			}
			byLabel[w.Label] = append(byLabel[w.Label], windowPoint{at: r.At, demand: w.UsedPct / 100 * f})
			resets[w.Label] = append(resets[w.Label], w.ResetsAt)
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
				cur = &windowInstance{label: label, reset: rst[i]}
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
	// overflowShare is the share of the period's demand above the tier's
	// allowance, in the window kind where it is largest.
	overflowShare float64
}

func simulate(ws []windowInstance, factor float64, until time.Time) simulation {
	var s simulation
	var spans [][2]time.Time
	total, over := map[string]float64{}, map[string]float64{}
	for _, w := range ws {
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
		end := w.reset
		if end.After(until) {
			end = until
		}
		if end.After(at) {
			spans = append(spans, [2]time.Time{at, end})
		}
	}
	for label, t := range total {
		if t > 0 {
			s.overflowShare = math.Max(s.overflowShare, over[label]/t)
		}
	}
	s.blocked = unionLength(spans)
	return s
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
		Since: in.Since, Until: in.Until, Confidence: ConfidenceLow}
	factorAt := in.FactorAt
	if factorAt == nil {
		factorAt = func(time.Time) (float64, bool) { return in.Current.Factor, in.Current.Factor > 0 }
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
		s := simulate(ws, t.Factor, in.Until)
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
	a.Recommendation = recommend(a.Options, cur, perMonth)
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
	if days < adviceMinDays || (weekly > 0 && weekly < 2) {
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
			"window shares by those multiples and assumes every window, the weekly ones too, scales the same way.",
			joinAnd(derived), base, base))
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

func recommend(os []Option, cur *Option, perMonth float64) *Recommendation {
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
			r.Summary = fmt.Sprintf("%s ran out %d time(s) (about %s blocked) and is the top tier: no larger plan to move to, "+
				"so it stays; the lockouts are the price of this tier's allowance.",
				cur.Display, cur.Lockouts, hoursWords(cur.BlockedMinutes))
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

func ptrFloat(v float64) *float64 { return &v }

func round2(v float64) float64 { return math.Round(v*100) / 100 }
