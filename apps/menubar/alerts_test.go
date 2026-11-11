package main

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

// glanceAt is a glance with Claude's week and session and a pay-as-you-go
// limit at the given shares used.
func glanceAt(week, session, spend float64, weekResets string) json.RawMessage {
	return json.RawMessage(fmt.Sprintf(`{"plan_headroom":{"reports":[
	  {"provider":"anthropic","windows":[
	    {"name":"week","used_pct":%v,"resets_in":%q,"pace":{"status":"ahead","lasts_to_reset":false,"runs_out_in_ns":7200000000000}},
	    {"name":"5h","used_pct":%v,"resets_in":"2h0m0s"}]},
	  {"provider":"fireworks","display":"Pay as you go","spend_usd":%v,"spend_limit_usd":100,"spend_pct":%v}]}}`,
		week, weekResets, session, spend, spend))
}

func titles(ns []note) string {
	out := make([]string, 0, len(ns))
	for _, n := range ns {
		out = append(out, n.Title)
	}
	return strings.Join(out, " | ")
}

// The first reading only sets the baseline: an app opened at login does
// not repeat what was already true.
func TestAlertsStayQuietOnTheFirstReading(t *testing.T) {
	var a alerter
	if ns := a.observe(glanceAt(97, 10, 99, "30h0m0s")); len(ns) != 0 {
		t.Errorf("first reading alerted: %s", titles(ns))
	}
}

// Crossing 20% left, then 5%, then nothing, alerts once each; staying
// put or jumping two steps at once alerts once.
func TestAlertsOnEachStepDown(t *testing.T) {
	var a alerter
	a.observe(glanceAt(50, 10, 10, "30h0m0s"))
	steps := []struct {
		week, spend float64
		want        string
	}{
		{70, 10, ""},
		{81, 10, "Claude weekly window: 19% left"},
		{85, 10, ""},
		{96, 10, "Claude weekly window: 4% left"},
		{100, 10, "Claude weekly window is used up"},
		{100, 10, ""},
		{100, 97, "Fireworks spend limit: 3% left"},
	}
	for _, s := range steps {
		if got := titles(a.observe(glanceAt(s.week, 10, s.spend, "30h0m0s"))); got != s.want {
			t.Errorf("week %v spend %v: alerts %q, want %q", s.week, s.spend, got, s.want)
		}
	}
}

// An account-wide spend (every member of an organisation together) is
// named as the account's, not the operator's.
func TestAccountWideSpendIsNamedAsTheAccounts(t *testing.T) {
	g := glanceView{}
	g.PlanHeadroom.Reports = []report{{Provider: "fireworks", SpendUSD: 1000, SpendLimitUSD: 4000, SpendPct: 25,
		SpendScope: "account", SpendScopeLabel: "account acme-test, all members"}}
	ms := measures(g)
	if len(ms) != 1 || ms[0].words != "Fireworks account spend limit (account acme-test, all members)" {
		t.Errorf("measures %+v", ms)
	}
}

// A window whose source stopped, or that has reset since its reading, is
// not measured: its share would raise an alert about a window nobody read.
func TestAWindowThatIsNotCurrentIsNotMeasured(t *testing.T) {
	g := glanceView{}
	g.PlanHeadroom.Reports = []report{{Provider: "openai", Windows: []window{
		{Name: "5h", UsedPct: 96, Lapsed: true}, {Name: "day", UsedPct: 96, Stale: true}, {Name: "week", UsedPct: 40},
	}}}
	if ms := measures(g); len(ms) != 1 || ms[0].used != 40 {
		t.Errorf("measures %+v, want the week alone", ms)
	}
}

// The body says when the window comes back, and, while it is still
// running, when it runs out at this pace.
func TestAlertBodies(t *testing.T) {
	var a alerter
	a.observe(glanceAt(50, 10, 10, "30h0m0s"))
	ns := a.observe(glanceAt(81, 10, 10, "30h0m0s"))
	if len(ns) != 1 || ns[0].Body != "Runs out in 2h 0m at this pace; resets in 1d 6h." {
		t.Errorf("warning %+v", ns)
	}
	ns = a.observe(glanceAt(100, 10, 10, "30h0m0s"))
	if len(ns) != 1 || ns[0].Body != "Resets in 1d 6h." {
		t.Errorf("used up %+v", ns)
	}
}

// A window that had warned and comes back says so, once; one that never
// warned resets silently.
func TestAlertsOnReset(t *testing.T) {
	var a alerter
	a.observe(glanceAt(50, 60, 10, "30h0m0s"))
	a.observe(glanceAt(100, 10, 10, "30h0m0s"))
	if got := titles(a.observe(glanceAt(0, 0, 10, "167h0m0s"))); got != "Claude weekly window has reset" {
		t.Errorf("reset alerts %q", got)
	}
	if got := titles(a.observe(glanceAt(3, 2, 10, "166h0m0s"))); got != "" {
		t.Errorf("after the reset %q", got)
	}
}

// An unreadable answer or a daemon error changes nothing.
func TestAlertsIgnoreUnreadableAnswers(t *testing.T) {
	var a alerter
	a.observe(glanceAt(50, 10, 10, "30h0m0s"))
	if ns := a.observe(json.RawMessage(`not json`)); len(ns) != 0 {
		t.Errorf("garbage alerted %+v", ns)
	}
	if got := titles(a.observe(glanceAt(81, 10, 10, "30h0m0s"))); got != "Claude weekly window: 19% left" {
		t.Errorf("after garbage %q", got)
	}
}

// The switch is remembered across launches, and on until turned off.
func TestAlertSettingPersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "TokenOps", "menubar.json")
	if !loadSettings(path).alertsOn() {
		t.Error("alerts are off by default")
	}
	off := false
	if err := saveSettings(path, settings{Alerts: &off}); err != nil {
		t.Fatal(err)
	}
	if loadSettings(path).alertsOn() {
		t.Error("alerts stayed on after being turned off")
	}
}

// A plan whose every window has gone stale alerts once that its reading
// stopped, and once that it is back; a stale window alerts nothing else.
func TestAlertsWhenAPlansReadingStops(t *testing.T) {
	at := func(stale bool, used float64) json.RawMessage {
		return json.RawMessage(fmt.Sprintf(`{"plan_headroom":{"reports":[{"provider":"anthropic","windows":[
		  {"name":"week","used_pct":%v,"stale":%v,"observed_at":"2026-10-05T18:55:05Z"},
		  {"name":"5h","used_pct":10,"stale":%v,"observed_at":"2026-10-05T18:55:05Z"}]}]}}`, used, stale, stale))
	}
	var a alerter
	a.observe(at(false, 50))
	if got := titles(a.observe(at(true, 99))); got != "Claude's plan reading stopped" {
		t.Errorf("stopped: %q", got)
	}
	if got := titles(a.observe(at(true, 99))); got != "" {
		t.Errorf("repeated: %q", got)
	}
	if got := titles(a.observe(at(false, 50))); got != "Claude's plan reading is back" {
		t.Errorf("back: %q", got)
	}
}

// A spend figure whose reader has stopped is not measured: the last
// figure read would raise an alert about spend nobody read. A balance
// that is old beside a current spend takes nothing from the measure.
func TestASpendFigureThatIsNotCurrentIsNotMeasured(t *testing.T) {
	spend := report{Provider: "fireworks", SpendUSD: 96, SpendLimitUSD: 100, SpendPct: 96, SpendSource: "vendor"}
	for name, tc := range map[string]struct {
		stale  bool
		source string
		want   int
	}{
		"current":                      {false, "vendor", 1},
		"its reader stopped":           {true, "vendor", 0},
		"the balance beside it is old": {true, "estimate", 1},
		"current, the estimate":        {false, "estimate", 1},
	} {
		r := spend
		r.SpendStale, r.SpendSource = tc.stale, tc.source
		g := glanceView{}
		g.PlanHeadroom.Reports = []report{r}
		if ms := measures(g); len(ms) != tc.want {
			t.Errorf("%s: measures %+v, want %d", name, ms, tc.want)
		}
	}
}

// A plan with a spend limit and no window alerts once that its reading
// stopped, and once that it is back; the old figure alerts nothing else.
func TestAlertsWhenASpendReadingStops(t *testing.T) {
	at := func(stale bool, pct float64) json.RawMessage {
		return json.RawMessage(fmt.Sprintf(`{"plan_headroom":{"reports":[{"provider":"fireworks",
		  "spend_usd":%v,"spend_limit_usd":100,"spend_pct":%v,"spend_source":"vendor","spend_stale":%v}]}}`, pct, pct, stale))
	}
	var a alerter
	a.observe(at(false, 50))
	ns := a.observe(at(true, 99))
	if got := titles(ns); got != "Fireworks's plan reading stopped" {
		t.Errorf("stopped: %q", got)
	}
	if len(ns) == 1 && ns[0].Body != "Its spend shows the last reading. `tokenops status` names the source that stopped." {
		t.Errorf("body %q", ns[0].Body)
	}
	if got := titles(a.observe(at(true, 99))); got != "" {
		t.Errorf("repeated: %q", got)
	}
	if got := titles(a.observe(at(false, 50))); got != "Fireworks's plan reading is back" {
		t.Errorf("back: %q", got)
	}

	// A plan with neither a window nor a limit has no reading to stop.
	var b alerter
	none := func(stale bool) json.RawMessage {
		return json.RawMessage(fmt.Sprintf(`{"plan_headroom":{"reports":[{"provider":"fireworks","spend_usd":3,"spend_source":"vendor","spend_stale":%v}]}}`, stale))
	}
	b.observe(none(false))
	if got := titles(b.observe(none(true))) + titles(b.observe(none(false))); got != "" {
		t.Errorf("no limit: %q", got)
	}
}
