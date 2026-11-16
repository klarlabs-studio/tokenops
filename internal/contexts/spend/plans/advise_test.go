package plans

import (
	"math"
	"strconv"
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
		{"claude-max", "claude-pro", 5, true},
		{"claude-ultra", "claude-pro", 10, true},   // weekly: Ultra has no 5-hour limit
		{"claude-max-20x", "claude-pro", 10, true}, // Ultra's former name
		{"claude-max-5x", "claude-pro", 5, true},   // Max's former name
		{"claude-enterprise", "", 0, false},        // billed at API rates: no window to scale
		{"gpt-plus", "gpt-plus", 1, true},
		{"gpt-pro-5x", "gpt-plus", 10, true}, // Pro 100: weekly only
		{"gpt-pro", "", 0, false},            // tier unknown: no multiple
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
	fam := TierFamily("claude-ultra")
	names := make([]string, 0, len(fam))
	for _, tier := range fam {
		names = append(names, tier.Name)
	}
	// Team seats are bought per member with a minimum, not by one person
	// moving down a tier; Enterprise is billed at API rates.
	if got := strings.Join(names, ","); got != "claude-pro,claude-max,claude-ultra" {
		t.Fatalf("family = %s", got)
	}
	if fam[0].Derived || !fam[1].Derived || fam[2].MonthlyUSD != 200 || fam[2].PriceSource != "list" {
		t.Errorf("tiers = %+v", fam)
	}
	// Max is five times Pro in both windows; Ultra ten times Pro's week,
	// with no 5-hour limit.
	if fam[1].Factor != 5 || fam[1].SessionFactor != 5 || fam[2].Factor != 10 || !fam[2].NoSessionLimit {
		t.Errorf("tiers = %+v", fam)
	}
	if f, limited := fam[2].Allowance(5 * time.Hour); limited || f != 0 {
		t.Errorf("Ultra's 5-hour allowance = %v %v, want none", f, limited)
	}
	if f, limited := fam[2].Allowance(week); !limited || f != 10 {
		t.Errorf("Ultra's weekly allowance = %v %v, want 10", f, limited)
	}
	if TierFamily("claude-max-20x")[2].Name != "claude-ultra" {
		t.Error("a retired name's family is not the plan's")
	}
	// A team seat's own family keeps the seat itself.
	team := TierFamily("claude-team-premium")
	if len(team) != 4 || team[2].Name != "claude-team-premium" {
		t.Errorf("team family = %+v", team)
	}
	if TierFamily("gpt-pro") != nil {
		t.Error("a plan with no modelled multiple has no family")
	}
	// ChatGPT's Pro tiers are multiples of Plus's week, with no 5-hour
	// limit; Business is per seat and Pro of an unknown tier has no multiple.
	gpt := TierFamily("gpt-plus")
	got := make([]string, 0, len(gpt))
	for _, tier := range gpt {
		got = append(got, tier.Name)
	}
	if strings.Join(got, ",") != "gpt-plus,gpt-pro-5x,gpt-pro-20x,gpt-pro-500" {
		t.Fatalf("ChatGPT family = %v", got)
	}
	if gpt[0].SessionFactor != 1 || gpt[3].Factor != 50 || !gpt[3].NoSessionLimit || gpt[3].SessionFactor != 0 {
		t.Errorf("ChatGPT tiers = %+v", gpt)
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

// maxTiers is a family at 1, 5 and 20 times its base, every window scaling
// alike: the replay's arithmetic, apart from what any vendor sells now.
// The names are Anthropic's retired ones, which still order by allowance.
func maxTiers() (pro, five, twenty Tier) {
	return Tier{Name: "claude-pro", Display: "Claude Pro", Factor: 1, SessionFactor: 1, MonthlyUSD: 20, PriceSource: "list"},
		Tier{Name: "claude-max-5x", Display: "Claude Max 5x", Factor: 5, SessionFactor: 5, Derived: true, MonthlyUSD: 100, PriceSource: "list"},
		Tier{Name: "claude-max-20x", Display: "Claude Max 20x", Factor: 20, SessionFactor: 20, Derived: true, MonthlyUSD: 200, PriceSource: "list"}
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
		FactorAt: func(time.Time, time.Duration) (float64, bool) { return 20, true },
		Current:  twenty, Candidates: []Tier{pro, five, twenty},
		ListValue: flatValue(280),
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
		FactorAt: func(time.Time, time.Duration) (float64, bool) { return 20, true },
		Current:  twenty, Candidates: []Tier{pro, five, twenty},
		ListValue: flatValue(4000),
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
		FactorAt: func(time.Time, time.Duration) (float64, bool) { return 5, true },
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
		FactorAt: func(at time.Time, _ time.Duration) (float64, bool) {
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
		FactorAt: func(time.Time, time.Duration) (float64, bool) { return 20, true },
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
		FactorAt: func(time.Time, time.Duration) (float64, bool) { return 20, true }, Current: twenty, Candidates: []Tier{pro, twenty}})
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
		FactorAt: func(time.Time, time.Duration) (float64, bool) { return 20, true },
		Current:  twenty, Candidates: []Tier{pro, five, twenty},
		ListValue: flatValue(4000),
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
		FactorAt: func(time.Time, time.Duration) (float64, bool) { return 20, true }, Current: twenty, Candidates: []Tier{twenty},
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
		FactorAt: func(time.Time, time.Duration) (float64, bool) { return 20, true },
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

// A Codex limit other than the default is its own bucket, not the plan's
// window, so replaying it against the Pro multiples would misread it.
func TestQuotaReadingsFromKeepsOnlyCodexDefaultLimit(t *testing.T) {
	now := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	reset := strconv.FormatInt(now.Add(48*time.Hour).Unix(), 10)
	reading := func(limit string) *eventschema.Envelope {
		attrs := map[string]string{"plan_type": "prolite",
			"primary_used_pct": "40", "primary_window_min": "10080", "primary_resets_at": reset}
		if limit != "" {
			attrs["limit_id"] = limit
		}
		return &eventschema.Envelope{Source: "codex-app-server", Timestamp: now.Add(-time.Hour), Attributes: attrs,
			Payload: &eventschema.PromptEvent{Provider: eventschema.ProviderOpenAI}}
	}
	for _, tc := range []struct {
		limit string
		want  int
	}{
		{"", 1},
		{DefaultCodexLimit, 1},
		{"premium", 0},
	} {
		got := QuotaReadingsFrom([]*eventschema.Envelope{reading(tc.limit)}, "openai", now.Add(-24*time.Hour), now)
		if len(got) != tc.want {
			t.Errorf("limit %q: %d readings, want %d", tc.limit, len(got), tc.want)
		}
	}
}

// With no readings the replay names the reader that reports the provider's
// windows, not another vendor's.
func TestReplayWithoutReadingsNamesTheProvidersMeter(t *testing.T) {
	for _, tc := range []struct{ provider, plan, want, not string }{
		{"anthropic", "claude-max", "vendor-usage setup claude-subscription", "codex"},
		{"openai", "gpt-plus", "codex_jsonl", "claude-subscription"},
	} {
		tier, _, _ := tierOf(tc.plan)
		a := Replay(ReplayInput{Provider: tc.provider, Current: tier, Candidates: TierFamily(tc.plan)})
		got := strings.Join(a.Caveats, " ")
		if !strings.Contains(got, tc.want) || strings.Contains(got, tc.not) {
			t.Errorf("%s: caveats = %q, want %q and not %q", tc.provider, got, tc.want, tc.not)
		}
	}
}

// The derived-tier caveat describes the provider's own base: Anthropic
// stopped publishing Pro's allowance, OpenAI states Plus's as
// model-dependent, and its Pro multiples are its own statement.
func TestDerivedCaveatDescribesTheProvidersBase(t *testing.T) {
	for _, tc := range []struct{ provider, plan, want, not string }{
		{"anthropic", "claude-max", "no longer publishes", "model-dependent"},
		{"openai", "gpt-pro-5x", "model-dependent", "no longer publishes"},
	} {
		tier, _, _ := tierOf(tc.plan)
		got := strings.Join(caveats(ReplayInput{Provider: tc.provider, Current: tier}, nil, TierFamily(tc.plan), listValue{}), " ")
		if !strings.Contains(got, tc.want) || strings.Contains(got, tc.not) {
			t.Errorf("%s: caveats = %q, want %q and not %q", tc.provider, got, tc.want, tc.not)
		}
	}
}

// With no window readings but the usage priced, the replay still weighs
// the fee against the API at list price, at low confidence: lockouts are
// unknown, so no tier is compared.
func TestReplayWithoutReadingsWeighsTheFeeAgainstTheAPI(t *testing.T) {
	until := time.Date(2026, 10, 14, 0, 0, 0, 0, time.UTC)
	since := until.Add(-30 * 24 * time.Hour)
	_, five, _ := maxTiers()
	for _, tc := range []struct {
		name     string
		value    float64
		verdict  string
		kind     string
		material bool
		multiple float64
	}{
		// $15 of usage in 30 days is $15.22 a month against a $100 fee.
		{"barely used", 15, VerdictSwitch, OptionAPI, true, 0.15},
		// $95 a month at list price saves $4.6: not worth a switch.
		{"close to the fee", 95 * 30 / averageMonthDays, VerdictStay, OptionPlan, false, 0.95},
		{"worth the fee", 400, VerdictStay, OptionPlan, false, 4.06},
	} {
		a := Replay(ReplayInput{Provider: "anthropic", Since: since, Until: until, Current: five,
			Candidates: []Tier{five}, ListValue: flatValue(tc.value)})
		r := a.Recommendation
		if r == nil || r.Verdict != tc.verdict || r.Kind != tc.kind || r.Material != tc.material {
			t.Errorf("%s: recommendation = %+v", tc.name, r)
			continue
		}
		if a.Confidence != ConfidenceLow || a.WindowsMeasured != 0 || a.Days != 30 {
			t.Errorf("%s: confidence %s, windows %d, days %v", tc.name, a.Confidence, a.WindowsMeasured, a.Days)
		}
		if len(a.Options) != 2 || optionFor(t, a, OptionAPI, "").MonthlyUSD == nil {
			t.Errorf("%s: options = %+v, want the plan and the API only", tc.name, a.Options)
		}
		if a.ValueMultiple == nil || math.Abs(*a.ValueMultiple-tc.multiple) > 0.01 {
			t.Errorf("%s: value multiple = %v, want %.2f", tc.name, a.ValueMultiple, tc.multiple)
		}
		if !strings.Contains(r.Summary, "no window readings") || strings.Contains(r.Summary, "fits") {
			t.Errorf("%s: summary = %q", tc.name, r.Summary)
		}
		if a.WorthSaying() {
			t.Errorf("%s: a low-confidence answer is not for an ambient surface", tc.name)
		}
	}
	// Unpriced usage, or a fee unknown, still gives nothing to weigh.
	a := Replay(ReplayInput{Provider: "anthropic", Since: since, Until: until, Current: five, Candidates: []Tier{five}})
	if a.Recommendation != nil || len(a.Options) != 0 {
		t.Errorf("unpriced: %+v", a)
	}
	five.MonthlyUSD = 0
	a = Replay(ReplayInput{Provider: "anthropic", Since: since, Until: until, Current: five, Candidates: []Tier{five},
		ListValue: flatValue(15)})
	if a.Recommendation != nil {
		t.Errorf("no fee: %+v", a.Recommendation)
	}
}

// The value multiple is the plan-covered usage at list price over what the
// plan cost for the same days.
func TestReplayValueMultiple(t *testing.T) {
	until := time.Date(2026, 10, 14, 0, 0, 0, 0, time.UTC)
	since := until.Add(-28 * 24 * time.Hour)
	pro, five, twenty := maxTiers()
	rs := make([]QuotaReading, 0, 8)
	for i := range 4 {
		reset := since.Add(time.Duration(i+1) * week)
		rs = append(rs, weekReadings(reset, []time.Duration{24 * time.Hour, 6 * 24 * time.Hour}, []float64{40, 90})...)
	}
	in := ReplayInput{
		Provider: "anthropic", Since: since, Until: until, Readings: rs,
		FactorAt: func(time.Time, time.Duration) (float64, bool) { return 20, true },
		Current:  twenty, Candidates: []Tier{pro, five, twenty},
		ListValue: flatValue(400),
	}
	a := Replay(in)
	// The readings cover 27 days: $400 against $200 x 27/30.44.
	want := 400 / (200 * 27 / averageMonthDays)
	if a.ValueMultiple == nil || math.Abs(*a.ValueMultiple-math.Round(want*100)/100) > 1e-9 {
		t.Errorf("value multiple = %v, want %.2f", a.ValueMultiple, want)
	}
	in.ListValue = nil
	if a := Replay(in); a.ValueMultiple != nil {
		t.Errorf("unpriced value multiple = %v", *a.ValueMultiple)
	}
}

// flatValue prices any span at usd: the plan-covered usage all falls in
// the days the replay prices.
func flatValue(usd float64) func(from, to time.Time) (float64, int) {
	return func(time.Time, time.Time) (float64, int) { return usd, 0 }
}

// dailyValue prices a span by the whole days of perDay it holds, the first
// starting at since.
func dailyValue(since time.Time, perDay []float64) func(from, to time.Time) (float64, int) {
	return func(from, to time.Time) (float64, int) {
		var usd float64
		for i, v := range perDay {
			if at := since.Add(time.Duration(i) * 24 * time.Hour); !at.Before(from) && !at.After(to) {
				usd += v
			}
		}
		return usd, 0
	}
}

// The API option, the overflow option and the value multiple price the
// usage of the days the readings cover, the demand the tiers are replayed
// on, whether or not the period has usage before the readings start.
func TestReplayPricesTheValueOverTheDaysReplayed(t *testing.T) {
	until := time.Date(2026, 10, 14, 0, 0, 0, 0, time.UTC)
	since := until.Add(-28 * 24 * time.Hour)
	pro, five, twenty := maxTiers()
	days := func(first, last int, usd float64) []float64 {
		out := make([]float64, 28)
		for i := first; i <= last; i++ {
			out[i] = usd
		}
		return out
	}
	cases := []struct {
		name string
		// fromWeek is the first of the period's four weeks with readings.
		fromWeek int
		peaks    []float64
		perDay   []float64
		days     float64
		// daily is the plan-covered usage a day over the days replayed.
		daily float64
		// share is the overflow share on Max 5x: the points over its cap,
		// in Pro windows, of the demand the readings show.
		share float64
	}{
		{"readings cover the period", 0, []float64{31, 20, 24, 10}, days(0, 27, 10), 28, 10, 1.2 / 17},
		{"readings start halfway, usage throughout", 2, []float64{31, 10}, days(0, 27, 10), 14, 10, 1.2 / 8.2},
		{"readings start halfway, usage only since", 2, []float64{31, 10}, days(14, 27, 20), 14, 20, 1.2 / 8.2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var rs []QuotaReading
			for i, p := range tc.peaks {
				reset := since.Add(time.Duration(tc.fromWeek+i+1) * week)
				rs = append(rs, weekReadings(reset, []time.Duration{0, 4 * 24 * time.Hour}, []float64{p / 4, p})...)
			}
			a := Replay(ReplayInput{
				Provider: "anthropic", Since: since, Until: until, Readings: rs,
				FactorAt: func(time.Time, time.Duration) (float64, bool) { return 20, true },
				Current:  twenty, Candidates: []Tier{pro, five, twenty},
				ListValue: dailyValue(since, tc.perDay),
			})
			if a.Days != tc.days {
				t.Fatalf("days = %v, want %v", a.Days, tc.days)
			}
			value := tc.daily * tc.days
			if a.ListValueUSD == nil || *a.ListValueUSD != value {
				t.Errorf("list value = %v, want %.2f", deref(a.ListValueUSD), value)
			}
			perMonth := tc.daily * averageMonthDays
			if api := optionFor(t, a, OptionAPI, ""); api.MonthlyUSD == nil || math.Abs(*api.MonthlyUSD-perMonth) > 0.01 {
				t.Errorf("api = %v, want %.2f", deref(api.MonthlyUSD), perMonth)
			}
			ov := optionFor(t, a, OptionPlanOverflow, "claude-max-5x")
			if ov.MeteredUSD == nil || math.Abs(*ov.MeteredUSD-tc.share*perMonth) > 0.01 {
				t.Errorf("overflow metered = %v, want %.2f", deref(ov.MeteredUSD), tc.share*perMonth)
			}
			multiple := math.Round(perMonth/200*100) / 100
			if a.ValueMultiple == nil || *a.ValueMultiple != multiple {
				t.Errorf("value multiple = %v, want %.2f", deref(a.ValueMultiple), multiple)
			}
		})
	}
}

// With no readings, the fee-versus-API replay prices the days the event
// history covers: a period reaching back before the first stored event
// would spread the usage over days nothing was recorded on, and the API
// option per month would come out too low.
func TestReplayWithoutReadingsPricesOnlyTheDaysWithHistory(t *testing.T) {
	until := time.Date(2026, 10, 14, 0, 0, 0, 0, time.UTC)
	since := until.Add(-60 * 24 * time.Hour)
	_, five, _ := maxTiers()
	for _, tc := range []struct {
		name        string
		historyFrom time.Time
		days        float64
	}{
		{"history unknown", time.Time{}, 60},
		{"history reaches back past the period", since.Add(-24 * time.Hour), 60},
		{"history starts 28 days back", until.Add(-28 * 24 * time.Hour), 28},
		{"history starts after the period ends", until.Add(time.Hour), 60},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var priced [2]time.Time
			a := Replay(ReplayInput{Provider: "anthropic", Since: since, Until: until, HistoryFrom: tc.historyFrom,
				Current: five, Candidates: []Tier{five},
				ListValue: func(from, to time.Time) (float64, int) {
					priced = [2]time.Time{from, to}
					return 280, 0
				}})
			if a.Days != tc.days {
				t.Fatalf("days = %v, want %v", a.Days, tc.days)
			}
			if want := until.Add(-time.Duration(tc.days*24) * time.Hour); !priced[0].Equal(want) || !priced[1].Equal(until) {
				t.Errorf("priced %v..%v, want %v..%v", priced[0], priced[1], want, until)
			}
			perMonth := 280 * averageMonthDays / tc.days
			if api := optionFor(t, a, OptionAPI, ""); api.MonthlyUSD == nil || math.Abs(*api.MonthlyUSD-perMonth) > 0.01 {
				t.Errorf("api = %v, want %.2f", deref(api.MonthlyUSD), perMonth)
			}
			multiple := math.Round(280/(five.MonthlyUSD*tc.days/averageMonthDays)*100) / 100
			if a.ValueMultiple == nil || *a.ValueMultiple != multiple {
				t.Errorf("value multiple = %v, want %.2f", deref(a.ValueMultiple), multiple)
			}
		})
	}
}
