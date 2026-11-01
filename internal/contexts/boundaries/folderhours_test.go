package boundaries

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// withHome points ~ at home for the test.
func withHome(t *testing.T, home string) {
	t.Helper()
	prev := userHomeDir
	userHomeDir = func() (string, error) { return home, nil }
	t.Cleanup(func() { userHomeDir = prev })
}

func folderRule(t *testing.T, folder string) Rule {
	t.Helper()
	rules, err := Parse([]RuleSpec{{Name: "r", Action: "continue", Require: "ask", Stage: "enforce", When: map[string]string{"folder": folder}}})
	if err != nil {
		t.Fatal(err)
	}
	return rules[0]
}

func TestFolderMatchesTheFolderAndWhatIsInsideIt(t *testing.T) {
	withHome(t, "/home/op")
	cases := []struct {
		name   string
		folder string
		cwd    string
		want   bool
	}{
		{"the folder itself", "/w/acme", "/w/acme", true},
		{"nested inside", "/w/acme", "/w/acme/api/internal", true},
		{"sibling with the same prefix", "/w/acme", "/w/acme-old", false},
		{"outside", "/w/acme", "/w/other", false},
		{"parent of the folder", "/w/acme", "/w", false},
		{"trailing slash on the rule", "/w/acme/", "/w/acme/api", true},
		{"trailing slash on the cwd", "/w/acme", "/w/acme/", true},
		{"unclean cwd", "/w/acme", "/w/other/../acme/./api", true},
		{"tilde expanded", "~/work", "/home/op/work/site", true},
		{"tilde alone", "~", "/home/op/anything", true},
		{"one of several", "/w/one, ~/two", "/home/op/two/x", true},
		{"none of several", "/w/one, /w/two", "/w/three", false},
		{"root holds everything", "/", "/w/acme", true},
		{"case matters", "/w/Acme", "/w/acme", false},
		{"glob", "/w/clients/*", "/w/clients/acme/api", true},
		{"glob outside", "/w/clients/*", "/w/home/acme", false},
		{"glob does not match the parent", "/w/clients/*", "/w/clients", false},
		{"unknown cwd", "/w/acme", "", false},
		{"relative cwd", "/w/acme", "acme", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := folderRule(t, tc.folder)
			_, got := r.Match(ActionContinue, Facts{Folder: tc.cwd})
			if got != tc.want {
				t.Fatalf("folder %q, cwd %q: matched = %v; want %v", tc.folder, tc.cwd, got, tc.want)
			}
		})
	}
}

func hoursRule(t *testing.T, hours string) Rule {
	t.Helper()
	rules, err := Parse([]RuleSpec{{Name: "r", Action: "continue", Require: "ask", Stage: "enforce", When: map[string]string{"hours": hours}}})
	if err != nil {
		t.Fatal(err)
	}
	return rules[0]
}

func TestHoursHoldInsideTheirWindows(t *testing.T) {
	berlin := time.FixedZone("CET", 3600)
	// 2026-11-02 is a Monday.
	at := func(day, hour, minute int) time.Time { return time.Date(2026, 11, 2+day, hour, minute, 0, 0, berlin) }
	cases := []struct {
		name  string
		hours string
		now   time.Time
		want  bool
	}{
		{"inside", "09:00-18:00", at(0, 12, 0), true},
		{"at the start", "09:00-18:00", at(0, 9, 0), true},
		{"at the end, which is out", "09:00-18:00", at(0, 18, 0), false},
		{"before", "09:00-18:00", at(0, 8, 59), false},
		{"across midnight, late", "22:00-06:00", at(0, 23, 30), true},
		{"across midnight, early", "22:00-06:00", at(1, 5, 59), true},
		{"across midnight, daytime", "22:00-06:00", at(0, 12, 0), false},
		{"weekday inside", "mon-fri 09:00-18:00", at(4, 10, 0), true},
		{"weekend outside", "mon-fri 09:00-18:00", at(5, 10, 0), false},
		{"weekdays alone", "sat-sun", at(6, 3, 0), true},
		{"weekdays alone, a weekday", "sat-sun", at(0, 3, 0), false},
		{"range wrapping the week", "fri-mon 08:00-09:00", at(6, 8, 30), true},
		{"days joined by +", "mon+wed 08:00-09:00", at(2, 8, 30), true},
		{"days joined by +, another day", "mon+wed 08:00-09:00", at(1, 8, 30), false},
		{"night belongs to the day it starts", "fri 22:00-06:00", at(5, 2, 0), true},
		{"night after another day", "fri 22:00-06:00", at(4, 2, 0), false},
		{"one of several windows", "09:00-12:00, 13:00-17:00", at(0, 14, 0), true},
		{"between windows", "09:00-12:00, 13:00-17:00", at(0, 12, 30), false},
		{"to 24:00", "20:00-24:00", at(0, 23, 59), true},
		{"full names", "monday-friday 09:00-18:00", at(0, 10, 0), true},
		{"unknown time", "09:00-18:00", time.Time{}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := hoursRule(t, tc.hours)
			_, got := r.Match(ActionContinue, Facts{Now: tc.now})
			if got != tc.want {
				t.Fatalf("hours %q at %s: matched = %v; want %v", tc.hours, tc.now.Format("Mon 15:04"), got, tc.want)
			}
		})
	}
}

func TestHoursReadTheTimeInItsOwnZone(t *testing.T) {
	r := hoursRule(t, "09:00-18:00")
	utc := time.Date(2026, 11, 2, 7, 30, 0, 0, time.UTC)
	if _, ok := r.Match(ActionContinue, Facts{Now: utc}); ok {
		t.Fatal("07:30 UTC matched 09:00-18:00")
	}
	if _, ok := r.Match(ActionContinue, Facts{Now: utc.In(time.FixedZone("UTC+2", 2*3600))}); !ok {
		t.Fatal("09:30 local did not match 09:00-18:00")
	}
}

func TestFolderAndHoursMustBothHold(t *testing.T) {
	rules, err := Parse([]RuleSpec{{
		Name: "client-hours", Action: "continue", Require: "deny", Stage: "enforce",
		When: map[string]string{"folder": "/w/acme", "hours": "mon-fri 09:00-18:00", "session_cost_usd": ">= 5"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	monday := time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC)
	all := Facts{Folder: "/w/acme/api", Now: monday, SessionCostUSD: Known(6)}
	cases := []struct {
		name  string
		facts func(Facts) Facts
		want  bool
	}{
		{"every condition holds", func(f Facts) Facts { return f }, true},
		{"another folder", func(f Facts) Facts { f.Folder = "/w/home"; return f }, false},
		{"after hours", func(f Facts) Facts { f.Now = monday.Add(9 * time.Hour); return f }, false},
		{"under the cost", func(f Facts) Facts { f.SessionCostUSD = Known(1); return f }, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := Evaluate(rules, ActionContinue, tc.facts(all))
			if got := v.Refuses(); got != tc.want {
				t.Fatalf("refuses = %v; want %v", got, tc.want)
			}
		})
	}
	m, _ := rules[0].Match(ActionContinue, all)
	for _, want := range []string{"session cost $6.00", "working in /w/acme", "at Mon 10:00, within hours mon-fri 09:00-18:00"} {
		if !strings.Contains(m.Crossed(), want) {
			t.Errorf("crossed = %q; want it to contain %q", m.Crossed(), want)
		}
	}
	if len(m.Crossings()) != 1 {
		t.Errorf("crossings = %+v; folder and hours cross no figure", m.Crossings())
	}
}

func TestARuleWithoutFolderOrHoursAppliesEverywhere(t *testing.T) {
	r := Rule{Name: "r", Action: ActionContinue, Require: Require{Kind: RequireAsk}, Stage: StageEnforce}
	if _, ok := r.Match(ActionContinue, Facts{}); !ok {
		t.Fatal("a rule with no conditions did not match without a folder or a time")
	}
}

func TestFolderAndHoursRefuseBadValues(t *testing.T) {
	withHome(t, "/home/op")
	cases := []struct {
		name   string
		action string
		when   map[string]string
		want   string
	}{
		{"empty folder", "continue", map[string]string{"folder": ""}, "names an empty folder"},
		{"empty folder in a list", "continue", map[string]string{"folder": "/w/a,,/w/b"}, "names an empty folder"},
		{"relative folder", "continue", map[string]string{"folder": "work/acme"}, `folder "work/acme" is not absolute`},
		{"user tilde", "continue", map[string]string{"folder": "~bob/x"}, "is not absolute"},
		{"bad glob", "continue", map[string]string{"folder": "/w/[acme"}, "is not a valid glob"},
		{"folder with a comparator", "continue", map[string]string{"folder": "== /w/a"}, "takes a path, not a comparator"},
		{"folder on the router", "model_route", map[string]string{"folder": "/w/a"}, "when.folder does not apply to model_route rules"},
		{"folder on share", "share", map[string]string{"folder": "/w/a"}, "when.folder does not apply to share rules"},
		{"empty hours", "continue", map[string]string{"hours": ""}, "write it as [weekdays] HH:MM-HH:MM"},
		{"hour out of range", "continue", map[string]string{"hours": "09:00-25:00"}, `"25:00" is not a time of day`},
		{"minute out of range", "continue", map[string]string{"hours": "09:60-10:00"}, `"09:60" is not a time of day`},
		{"am pm", "continue", map[string]string{"hours": "9am-5pm"}, `"9am" is not a time like 09:00`},
		{"no end", "continue", map[string]string{"hours": "09:00"}, "needs a start and an end"},
		{"bad clock", "continue", map[string]string{"hours": "9-17:00"}, `"9" is not a time like 09:00`},
		{"24:00 as a start", "continue", map[string]string{"hours": "24:00-06:00"}, "is not a time of day"},
		{"empty window", "continue", map[string]string{"hours": "09:00-09:00"}, "starts and ends at the same time"},
		{"bad weekday", "continue", map[string]string{"hours": "mo-fr 09:00-18:00"}, `"mo-fr" is not a weekday`},
		{"unknown weekday", "continue", map[string]string{"hours": "funday"}, "is not a weekday"},
		{"weekday after the time", "continue", map[string]string{"hours": "09:00-18:00 mon"}, `"09:00-18:00" is not a weekday`},
		{"too many parts", "continue", map[string]string{"hours": "mon 09:00 18:00"}, "write it as"},
		{"hours with a comparator", "continue", map[string]string{"hours": ">= 09:00"}, "not a comparator"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse([]RuleSpec{{Name: "r", Action: tc.action, Require: "ask", When: tc.when}})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v; want it to contain %q", err, tc.want)
			}
			if !strings.Contains(err.Error(), `"r"`) {
				t.Errorf("err = %v; want it to name the rule", err)
			}
		})
	}
}

func TestHoursAndFolderApplyWhereTheyAreKnown(t *testing.T) {
	for _, action := range []string{"continue", "spend", "subagent_model", "model_route", "share"} {
		if _, err := Parse([]RuleSpec{{Name: "r", Action: action, Require: "ask", When: map[string]string{"hours": "09:00-18:00"}}}); err != nil {
			t.Errorf("hours on %s: %v", action, err)
		}
	}
	for _, action := range []string{"continue", "spend", "subagent_model"} {
		if _, err := Parse([]RuleSpec{{Name: "r", Action: action, Require: "ask", When: map[string]string{"folder": "/w"}}}); err != nil {
			t.Errorf("folder on %s: %v", action, err)
		}
	}
}

func TestConditionStringReadsLikeConfig(t *testing.T) {
	withHome(t, "/home/op")
	rules, err := Parse([]RuleSpec{{
		Name: "r", Action: "continue", Require: "ask",
		When: map[string]string{
			"session_cost_usd": ">= 20", "plan_left_pct": "<= 10", "window": "week",
			"folder": "~/work, /srv", "hours": "Mon-Fri 09:00-18:00, sat-sun, 22:00-06:00",
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	got := make([]string, 0, len(rules[0].When))
	for _, c := range rules[0].When {
		got = append(got, c.String())
	}
	want := []string{
		"session_cost_usd >= 20", "plan_left_pct <= 10 (week)", "folder ~/work, /srv",
		"hours mon-fri 09:00-18:00, sat-sun, 22:00-06:00",
	}
	if strings.Join(got, "; ") != strings.Join(want, "; ") {
		t.Fatalf("conditions = %q; want %q", got, want)
	}
	if f := rules[0].When[2].Folders; len(f) != 2 || f[0] != "/home/op/work" || f[1] != "/srv" {
		t.Errorf("folders = %q", f)
	}
}

func TestOrgConditionsOnlyNarrowTheOrgRule(t *testing.T) {
	// An organisation rule limited to a folder and hours applies less often
	// than one without; a local rule binds as before wherever it does not.
	local, err := Parse([]RuleSpec{{Name: "spend-cap", Action: "continue", Require: "deny", Stage: "enforce", When: map[string]string{"session_cost_usd": ">= 10"}}})
	if err != nil {
		t.Fatal(err)
	}
	org, err := Parse([]RuleSpec{{Name: "office", Action: "continue", Require: "allow", Stage: "enforce", When: map[string]string{"folder": "/w/acme", "hours": "09:00-18:00"}}})
	if err != nil {
		t.Fatal(err)
	}
	rules := append(local, FromOrg(org)...)
	f := Facts{SessionCostUSD: Known(12), Folder: "/w/acme", Now: time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC)}
	v := Evaluate(rules, ActionContinue, f)
	if !v.Refuses() || v.Binding.Rule.Name != "spend-cap" {
		t.Fatalf("binding = %+v; an organisation allow inside its folder and hours must not loosen a local deny", v.Binding)
	}
}

func TestATildeFolderParsesWithoutAHome(t *testing.T) {
	prev := userHomeDir
	userHomeDir = func() (string, error) { return "", errors.New("no home") }
	t.Cleanup(func() { userHomeDir = prev })
	r := folderRule(t, "~/work")
	if _, ok := r.Match(ActionContinue, Facts{Folder: "/home/op/work"}); ok {
		t.Fatal("an unexpanded ~ matched a directory")
	}
}
