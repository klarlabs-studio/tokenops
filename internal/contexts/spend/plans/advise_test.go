package plans

import (
	"math"
	"strings"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/pkg/eventschema"
)

func TestTierOf(t *testing.T) {
	cases := []struct {
		plan   string
		base   string
		factor float64
		ok     bool
	}{
		{"claude-pro", "claude-pro", 1, true},
		{"claude-max-5x", "claude-pro", 5, true},
		{"claude-max-20x", "claude-pro", 20, true},
		{"claude-max", "claude-pro", 20, true}, // deprecated alias
		{"claude-enterprise", "", 0, false},    // billed at API rates: no window to scale
		{"gpt-plus", "", 0, false},             // no multiple modelled
		{"nope", "", 0, false},
	}
	for _, c := range cases {
		base, factor, ok := TierOf(c.plan)
		if base != c.base || factor != c.factor || ok != c.ok {
			t.Errorf("TierOf(%q) = %q %v %v, want %q %v %v", c.plan, base, factor, ok, c.base, c.factor, c.ok)
		}
	}
}

func TestTierFamily(t *testing.T) {
	fam := TierFamily("claude-max-20x")
	names := make([]string, 0, len(fam))
	for _, tier := range fam {
		names = append(names, tier.Name)
	}
	// Team seats are bought per member with a minimum, not by one person
	// moving down a tier; Enterprise is billed at API rates.
	if got := strings.Join(names, ","); got != "claude-pro,claude-max-5x,claude-max-20x" {
		t.Fatalf("family = %s", got)
	}
	if fam[0].Derived || !fam[1].Derived || fam[2].MonthlyUSD != 200 || fam[2].PriceSource != "list" {
		t.Errorf("tiers = %+v", fam)
	}
	// A team seat's own family keeps the seat itself.
	team := TierFamily("claude-team-premium")
	if len(team) != 4 || team[2].Name != "claude-team-premium" {
		t.Errorf("team family = %+v", team)
	}
	if TierFamily("gpt-plus") != nil {
		t.Error("a plan with no modelled multiple has no family")
	}
}

// week builds one weekly window's readings: shares used on the plan in
// force, at offsets from the window's start.
func weekReadings(reset time.Time, at []time.Duration, used []float64) []QuotaReading {
	start := reset.Add(-week)
	out := make([]QuotaReading, len(at))
	for i := range at {
		out[i] = QuotaReading{At: start.Add(at[i]), Windows: []QuotaWindow{{Label: "weekly", UsedPct: used[i], ResetsAt: reset, Duration: week}}}
	}
	return out
}

func maxTiers() (pro, five, twenty Tier) {
	fam := TierFamily("claude-max-20x")
	return fam[0], fam[1], fam[2]
}

func optionFor(t *testing.T, a Advice, kind, plan string) Option {
	t.Helper()
	for _, o := range a.Options {
		if o.Kind == kind && o.Plan == plan {
			return o
		}
	}
	t.Fatalf("no %s option for %q in %+v", kind, plan, a.Options)
	return Option{}
}

func TestReplayScalesWindowsAcrossTiers(t *testing.T) {
	until := time.Date(2026, 10, 14, 0, 0, 0, 0, time.UTC)
	since := until.Add(-28 * 24 * time.Hour)
	_, five, twenty := maxTiers()
	pro, _, _ := maxTiers()
	rs := make([]QuotaReading, 0, 8)
	// Four weekly windows on Max 20x, peaking at 31%, 20%, 24% and 10%.
	// The readings start a day into the period, so they cover 27 days.
	peaks := []float64{31, 20, 24, 10}
	for i, p := range peaks {
		reset := since.Add(time.Duration(i+1) * week)
		rs = append(rs, weekReadings(reset, []time.Duration{24 * time.Hour, 4 * 24 * time.Hour}, []float64{p / 4, p})...)
	}
	in := ReplayInput{
		Provider: "anthropic", Since: since, Until: until, Readings: rs,
		FactorAt: func(time.Time) (float64, bool) { return 20, true },
		Current:  twenty, Candidates: []Tier{pro, five, twenty},
		ListValueUSD: 280, ListValueKnown: true,
	}
	a := Replay(in)

	cur := optionFor(t, a, OptionPlan, "claude-max-20x")
	if !cur.Current || cur.Lockouts != 0 || cur.PeakPct == nil || *cur.PeakPct != 31 || cur.PeakWindow != "weekly" {
		t.Errorf("current = %+v", cur)
	}
	// On Max 5x the first week peaks at 124%: it runs out between the day-1
	// reading (31%) and the day-4 one, and stays out until the reset.
	m5 := optionFor(t, a, OptionPlan, "claude-max-5x")
	if m5.Lockouts != 1 || m5.PeakPct == nil || *m5.PeakPct != 124 {
		t.Errorf("max 5x = %+v", m5)
	}
	// 31 -> 124 over three days; 100 is reached 69/93 of the way, at day
	// 1 + 3*69/93 = 3.226 days, so 3.774 days are blocked.
	wantBlocked := (7 - (1 + 3*69.0/93)) * 24 * 60
	if math.Abs(float64(m5.BlockedMinutes)-wantBlocked) > 1 {
		t.Errorf("blocked = %d min, want %.0f", m5.BlockedMinutes, wantBlocked)
	}
	if m5.MonthlyUSD == nil || *m5.MonthlyUSD != 100 || m5.SavingsUSD == nil || *m5.SavingsUSD != 100 {
		t.Errorf("max 5x cost = %v saving %v", m5.MonthlyUSD, m5.SavingsUSD)
	}
	// Max 5x with metered overflow: no lockouts; what goes over the cap is
	// priced as its share of the period's list value.
	ov := optionFor(t, a, OptionPlanOverflow, "claude-max-5x")
	if ov.Lockouts != 0 || ov.MeteredUSD == nil {
		t.Fatalf("overflow = %+v", ov)
	}
	// Overflow units: 124-100 = 24 points of a 5x window, i.e. 1.2 Pro
	// windows, of a total 85/100*20 = 17: 1.2/17 of $280 over 27 days.
	wantOver := 280 * (1.2 / 17) * averageMonthDays / 27
	if math.Abs(*ov.MeteredUSD-wantOver) > 0.01 {
		t.Errorf("overflow metered = %.2f, want %.2f", *ov.MeteredUSD, wantOver)
	}
	// Pro locks out every week.
	if p := optionFor(t, a, OptionPlan, "claude-pro"); p.Lockouts != 4 {
		t.Errorf("pro = %+v", p)
	}
	api := optionFor(t, a, OptionAPI, "")
	if api.MonthlyUSD == nil || math.Abs(*api.MonthlyUSD-280*averageMonthDays/27) > 0.01 {
		t.Errorf("api = %+v", api)
	}
	if a.Confidence != ConfidenceMedium {
		t.Errorf("confidence = %s, want medium: the tiers are derived", a.Confidence)
	}
	if len(a.Caveats) == 0 || !strings.Contains(a.Caveats[0], "unverified") {
		t.Errorf("caveats = %q, want the unverified baseline named", a.Caveats)
	}
	// Max 5x plus overflow is cheapest with no lockouts: ~$119.
	r := a.Recommendation
	if r == nil || r.Verdict != VerdictSwitch || r.Plan != "claude-max-5x" || r.Kind != OptionPlanOverflow {
		t.Fatalf("recommendation = %+v", r)
	}
	if !a.WorthSaying() {
		t.Error("a material saving with no lockouts is worth saying")
	}
	if !strings.Contains(r.Summary, "31%") || !strings.Contains(r.Summary, "Claude Max 5x") {
		t.Errorf("summary = %q", r.Summary)
	}
}

func TestReplayRecommendsStayingWhenNothingCheaperFits(t *testing.T) {
	until := time.Date(2026, 10, 14, 0, 0, 0, 0, time.UTC)
	since := until.Add(-28 * 24 * time.Hour)
	pro, five, twenty := maxTiers()
	rs := make([]QuotaReading, 0, 8)
	for i := range 4 {
		reset := since.Add(time.Duration(i+1) * week)
		rs = append(rs, weekReadings(reset, []time.Duration{24 * time.Hour, 6 * 24 * time.Hour}, []float64{40, 90})...)
	}
	a := Replay(ReplayInput{
		Provider: "anthropic", Since: since, Until: until, Readings: rs,
		FactorAt: func(time.Time) (float64, bool) { return 20, true },
		Current:  twenty, Candidates: []Tier{pro, five, twenty},
		ListValueUSD: 4000, ListValueKnown: true,
	})
	if r := a.Recommendation; r == nil || r.Verdict != VerdictStay || r.Plan != "claude-max-20x" {
		t.Fatalf("recommendation = %+v", a.Recommendation)
	}
	if a.WorthSaying() {
		t.Error("staying is not a finding")
	}
}

func TestReplayRecommendsUpgradeWhenTheCurrentPlanLocksOut(t *testing.T) {
	until := time.Date(2026, 10, 14, 0, 0, 0, 0, time.UTC)
	since := until.Add(-28 * 24 * time.Hour)
	pro, five, twenty := maxTiers()
	rs := make([]QuotaReading, 0, 8)
	for i := range 4 {
		reset := since.Add(time.Duration(i+1) * week)
		rs = append(rs, weekReadings(reset, []time.Duration{24 * time.Hour, 3 * 24 * time.Hour}, []float64{50, 100})...)
	}
	a := Replay(ReplayInput{
		Provider: "anthropic", Since: since, Until: until, Readings: rs,
		FactorAt: func(time.Time) (float64, bool) { return 5, true },
		Current:  five, Candidates: []Tier{pro, five, twenty},
	})
	cur := optionFor(t, a, OptionPlan, "claude-max-5x")
	if cur.Lockouts != 4 {
		t.Errorf("current = %+v, want the four real lockouts", cur)
	}
	if r := a.Recommendation; r == nil || r.Verdict != VerdictUpgrade || r.Plan != "claude-max-20x" {
		t.Fatalf("recommendation = %+v", a.Recommendation)
	}
	// Running out on the current plan hides the work that could not be
	// done, so the larger tier's figures are a floor; the caveat says so.
	found := false
	for _, c := range a.Caveats {
		found = found || strings.Contains(c, "floor")
	}
	if !found {
		t.Errorf("caveats = %q", a.Caveats)
	}
	// No list value: no API or overflow options to price.
	for _, o := range a.Options {
		if o.Kind != OptionPlan {
			t.Errorf("unpriced option %+v", o)
		}
	}
}

func TestReplayFollowsThePlanInForceAtEachReading(t *testing.T) {
	until := time.Date(2026, 10, 14, 0, 0, 0, 0, time.UTC)
	since := until.Add(-28 * 24 * time.Hour)
	pro, five, twenty := maxTiers()
	switched := since.Add(2 * week)
	rs := make([]QuotaReading, 0, 8)
	for i := range 4 {
		reset := since.Add(time.Duration(i+1) * week)
		// 80% on Max 5x before the switch, 20% on Max 20x after: the same
		// demand, 4 Pro windows a week.
		used := 80.0
		if reset.After(switched) {
			used = 20
		}
		rs = append(rs, weekReadings(reset, []time.Duration{24 * time.Hour, 3 * 24 * time.Hour}, []float64{used / 2, used})...)
	}
	a := Replay(ReplayInput{
		Provider: "anthropic", Since: since, Until: until, Readings: rs,
		FactorAt: func(at time.Time) (float64, bool) {
			if at.Before(switched) {
				return 5, true
			}
			return 20, true
		},
		Current: twenty, Candidates: []Tier{pro, five, twenty},
	})
	m5 := optionFor(t, a, OptionPlan, "claude-max-5x")
	if m5.Lockouts != 0 || m5.PeakPct == nil || *m5.PeakPct != 80 {
		t.Errorf("max 5x = %+v, want every week at 80%%", m5)
	}
}

func TestReplayWithTooLittleHistoryIsLowConfidence(t *testing.T) {
	until := time.Date(2026, 10, 14, 0, 0, 0, 0, time.UTC)
	pro, five, twenty := maxTiers()
	reset := until.Add(-24 * time.Hour)
	a := Replay(ReplayInput{
		Provider: "anthropic", Since: until.Add(-30 * 24 * time.Hour), Until: until,
		Readings: weekReadings(reset, []time.Duration{3 * 24 * time.Hour, 5 * 24 * time.Hour}, []float64{1, 2}),
		FactorAt: func(time.Time) (float64, bool) { return 20, true },
		Current:  twenty, Candidates: []Tier{pro, five, twenty},
	})
	if a.Confidence != ConfidenceLow {
		t.Errorf("confidence = %s, want low on four days of readings", a.Confidence)
	}
	if a.WorthSaying() {
		t.Error("low confidence is never a glance finding")
	}
	none := Replay(ReplayInput{Provider: "anthropic", Since: until.Add(-time.Hour), Until: until, Current: twenty, Candidates: []Tier{twenty}})
	if none.Confidence != ConfidenceLow || none.Recommendation != nil || len(none.Options) != 0 {
		t.Errorf("no readings = %+v", none)
	}
}

func TestReplayUnionsOverlappingLockouts(t *testing.T) {
	until := time.Date(2026, 10, 14, 0, 0, 0, 0, time.UTC)
	since := until.Add(-14 * 24 * time.Hour)
	reset := until.Add(-24 * time.Hour)
	fiveReset := reset.Add(-2 * time.Hour)
	start := reset.Add(-week)
	// A 5-hour window and the weekly one both run out on Pro at the same
	// moment; the blocked time counts once.
	rs := []QuotaReading{
		{At: start.Add(time.Hour), Windows: []QuotaWindow{{Label: "weekly", UsedPct: 1, ResetsAt: reset, Duration: week}}},
		{At: fiveReset.Add(-4 * time.Hour), Windows: []QuotaWindow{
			{Label: "weekly", UsedPct: 2, ResetsAt: reset, Duration: week},
			{Label: "5-hour", UsedPct: 1, ResetsAt: fiveReset, Duration: 5 * time.Hour}}},
		{At: fiveReset.Add(-3 * time.Hour), Windows: []QuotaWindow{
			{Label: "weekly", UsedPct: 30, ResetsAt: reset, Duration: week},
			{Label: "5-hour", UsedPct: 30, ResetsAt: fiveReset, Duration: 5 * time.Hour}}},
	}
	pro, _, twenty := maxTiers()
	a := Replay(ReplayInput{Provider: "anthropic", Since: since, Until: until, Readings: rs,
		FactorAt: func(time.Time) (float64, bool) { return 20, true }, Current: twenty, Candidates: []Tier{pro, twenty}})
	p := optionFor(t, a, OptionPlan, "claude-pro")
	if p.Lockouts != 2 {
		t.Fatalf("pro = %+v", p)
	}
	// The weekly one crosses 100% (5% on 20x) first and stays out until
	// its reset, which covers the 5-hour one's stretch.
	frac := (5.0 - 2) / (30 - 2)
	crossing := fiveReset.Add(-4 * time.Hour).Add(time.Duration(frac * float64(time.Hour)))
	want := reset.Sub(crossing).Minutes()
	if math.Abs(float64(p.BlockedMinutes)-want) > 1 {
		t.Errorf("blocked = %d, want %.0f", p.BlockedMinutes, want)
	}
}

func TestQuotaReadingsAndListValueFromEvents(t *testing.T) {
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	since := now.Add(-48 * time.Hour)
	reset := now.Add(24 * time.Hour)
	envs := []*eventschema.Envelope{
		{Source: "claude-usage-meter", Timestamp: now.Add(-time.Hour), Attributes: map[string]string{
			"granularity":    "quota_snapshot",
			"seven_day_kind": "weekly_all", "seven_day_used_pct": "40", "seven_day_reset_at": reset.Format(time.RFC3339),
		}, Payload: &eventschema.PromptEvent{Provider: eventschema.ProviderAnthropic}},
		{Source: "codex-jsonl", Timestamp: now.Add(-time.Hour), Attributes: map[string]string{
			"primary_used_pct": "10", "primary_window_min": "300", "primary_resets_at": "1999999999",
		}, Payload: &eventschema.PromptEvent{Provider: eventschema.ProviderOpenAI}},
		{Source: "claude-code-jsonl", Timestamp: now.Add(-2 * time.Hour), Attributes: map[string]string{"granularity": "assistant_turn"},
			Payload: &eventschema.PromptEvent{Provider: eventschema.ProviderAnthropic, CostSource: eventschema.CostSourcePlanIncluded, RequestModel: "a"}},
		{Source: "claude-code-jsonl", Timestamp: now.Add(-2 * time.Hour), Attributes: map[string]string{"granularity": "assistant_turn"},
			Payload: &eventschema.PromptEvent{Provider: eventschema.ProviderAnthropic, CostSource: eventschema.CostSourcePlanIncluded, RequestModel: "unknown"}},
		// Billed per token already: not what the plan carries.
		{Source: "proxy", Timestamp: now.Add(-2 * time.Hour),
			Payload: &eventschema.PromptEvent{Provider: eventschema.ProviderAnthropic, CostSource: eventschema.CostSourceMetered, CostUSD: 9}},
		// Before the period.
		{Source: "claude-code-jsonl", Timestamp: since.Add(-time.Hour),
			Payload: &eventschema.PromptEvent{Provider: eventschema.ProviderAnthropic, CostSource: eventschema.CostSourcePlanIncluded, RequestModel: "a"}},
	}
	rs := QuotaReadingsFrom(envs, "anthropic", since, now)
	if len(rs) != 1 || len(rs[0].Windows) != 1 || rs[0].Windows[0].UsedPct != 40 {
		t.Errorf("readings = %+v", rs)
	}
	price := func(p *eventschema.PromptEvent, _ time.Time) (float64, error) {
		if p.RequestModel == "unknown" {
			return 0, errNoRate
		}
		return 1.5, nil
	}
	usd, unpriced := ListValue(envs, "anthropic", since, now, price)
	if usd != 1.5 || unpriced != 1 {
		t.Errorf("list value = %v (%d unpriced)", usd, unpriced)
	}
}

type rateErr string

func (e rateErr) Error() string { return string(e) }

const errNoRate = rateErr("no rate")

// On the top tier with nothing higher to move to, a lockout must not
// become "upgrade" to the plan already in force. The only acceptable
// option is the same plan with metered overflow, which costs the same
// (no window went over its cap), so the verdict used to fall through to
// upgrade-to-self.
func TestReplayAtTheTopTierNeverUpgradesToItself(t *testing.T) {
	until := time.Date(2026, 10, 14, 0, 0, 0, 0, time.UTC)
	since := until.Add(-28 * 24 * time.Hour)
	pro, five, twenty := maxTiers()
	rs := make([]QuotaReading, 0, 8)
	for i := range 4 {
		reset := since.Add(time.Duration(i+1) * week)
		// Exactly 100% on the top tier: a lockout, with nothing over the cap.
		rs = append(rs, weekReadings(reset, []time.Duration{24 * time.Hour, 3 * 24 * time.Hour}, []float64{60, 100})...)
	}
	a := Replay(ReplayInput{
		Provider: "anthropic", Since: since, Until: until, Readings: rs,
		FactorAt: func(time.Time) (float64, bool) { return 20, true },
		Current:  twenty, Candidates: []Tier{pro, five, twenty},
		ListValueUSD: 4000, ListValueKnown: true,
	})
	if cur := optionFor(t, a, OptionPlan, "claude-max-20x"); cur.Lockouts == 0 {
		t.Fatalf("setup: current = %+v, want lockouts", cur)
	}
	r := a.Recommendation
	if r == nil {
		t.Fatal("no recommendation")
	}
	if r.Verdict == VerdictUpgrade || r.Plan == "claude-max-20x" && r.Verdict != VerdictStay {
		t.Fatalf("recommendation = %+v: top tier must not upgrade to itself", r)
	}
	if r.Verdict != VerdictStay || r.Plan != "claude-max-20x" {
		t.Errorf("recommendation = %+v, want stay on the top tier", r)
	}
	if !strings.Contains(r.Summary, "top tier") {
		t.Errorf("summary = %q, want the top tier named as the reason", r.Summary)
	}
	if a.WorthSaying() {
		t.Error("staying on the top tier is not a finding")
	}
}

// lockoutCycle is one weekly window of OpenAI that runs out at 100% and is
// reset early by the vendor: its readings, and the early reset.
func lockoutCycle(start time.Time) ([]QuotaReading, EarlyReset) {
	reset := start.Add(3 * 24 * time.Hour)
	early := start.Add(5 * time.Hour)
	rs := []QuotaReading{
		{At: start.Add(time.Hour), Windows: []QuotaWindow{{Label: "weekly", UsedPct: 60, ResetsAt: reset, Duration: week}}},
		{At: start.Add(2 * time.Hour), Windows: []QuotaWindow{{Label: "weekly", UsedPct: 100, ResetsAt: reset, Duration: week}}},
		{At: early, Windows: []QuotaWindow{{Label: "weekly", UsedPct: 2, ResetsAt: early.Add(week), Duration: week}}},
	}
	return rs, EarlyReset{Provider: "openai", Name: "weekly", Duration: week,
		PrevUsedPct: 100, PrevResetsAt: reset, PrevObservedAt: start.Add(2 * time.Hour),
		UsedPct: 2, ObservedAt: early, ResetsAt: early.Add(week)}
}

// A lockout the vendor ends early is blocked only until the reset, not
// until the window's scheduled reset.
func TestReplayEndsLockoutsAtAnEarlyReset(t *testing.T) {
	base := time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)
	rs, early := lockoutCycle(base)
	_, _, twenty := maxTiers()
	a := Replay(ReplayInput{Provider: "openai", Since: base, Until: base.Add(6 * 24 * time.Hour), Readings: rs,
		FactorAt: func(time.Time) (float64, bool) { return 20, true }, Current: twenty, Candidates: []Tier{twenty},
		EarlyResets: []EarlyReset{early}})
	cur := optionFor(t, a, OptionPlan, "claude-max-20x")
	if cur.Lockouts != 1 || cur.BlockedMinutes != 180 {
		t.Errorf("current = %+v, want one lockout blocked 180 minutes (exhausted at 2h, reset at 5h)", cur)
	}
}

// On the top tier with early resets, the summary says the vendor reset the
// window early and how much blocked time that cut out.
func TestReplaySaysWhenTheVendorResetTheTopTierEarly(t *testing.T) {
	base := time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)
	rs1, e1 := lockoutCycle(base)
	rs2, e2 := lockoutCycle(base.Add(8 * 24 * time.Hour))
	pro, five, twenty := maxTiers()
	a := Replay(ReplayInput{Provider: "openai", Since: base, Until: base.Add(14 * 24 * time.Hour),
		Readings: append(rs1, rs2...), EarlyResets: []EarlyReset{e1, e2},
		FactorAt: func(time.Time) (float64, bool) { return 20, true },
		Current:  twenty, Candidates: []Tier{pro, five, twenty}})
	r := a.Recommendation
	if r == nil || r.Verdict != VerdictStay || r.Plan != "claude-max-20x" {
		t.Fatalf("recommendation = %+v, want stay on the top tier", r)
	}
	for _, want := range []string{"top tier", "OpenAI reset the weekly window early both times", "about 6.0h"} {
		if !strings.Contains(r.Summary, want) {
			t.Errorf("summary = %q, want it to contain %q", r.Summary, want)
		}
	}
}
