package boundaries

import (
	"testing"
	"time"

	domain "go.klarlabs.de/tokenops/internal/contexts/boundaries"
)

// clientHours is a rule that refuses work in one client's folder during
// office hours.
func clientHours() domain.RuleSpec {
	return domain.RuleSpec{Name: "acme-office", Action: "continue", Require: "deny", Stage: "enforce",
		When: map[string]string{"folder": "/w/acme", "hours": "mon-fri 09:00-18:00"}}
}

func TestPauseAppliesAFolderAndHoursRuleOnlyThere(t *testing.T) {
	berlin := time.FixedZone("CEST", 2*3600)
	// testNow is Monday 2026-10-12 12:00 UTC: 14:00 in Berlin.
	cases := []struct {
		name    string
		folder  string
		now     time.Time
		loc     *time.Location
		verdict string
	}{
		{"in the folder, in hours", "/w/acme/api", testNow, berlin, VerdictDeny},
		{"in another folder", "/w/home", testNow, berlin, VerdictNone},
		{"folder unknown", "", testNow, berlin, VerdictNone},
		{"after hours", "/w/acme", testNow.Add(5 * time.Hour), berlin, VerdictNone},
		{"at the weekend", "/w/acme", testNow.Add(-48 * time.Hour), berlin, VerdictNone},
		{"read in the operator's zone", "/w/acme", time.Date(2026, 10, 12, 6, 30, 0, 0, time.UTC), berlin, VerdictNone},
		{"in hours in another zone", "/w/acme", time.Date(2026, 10, 12, 6, 30, 0, 0, time.UTC), time.FixedZone("UTC+3", 3*3600), VerdictDeny},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := testEnv(t, false)
			now := tc.now
			env.Now = func() time.Time { return now }
			env.Location = tc.loc
			c := call("s-" + tc.name)
			c.Folder = tc.folder
			res, err := Pause(rules(t, clientHours()), c, env)
			if err != nil {
				t.Fatal(err)
			}
			if res.Verdict != tc.verdict {
				t.Fatalf("verdict = %q (%s); want %q", res.Verdict, res.Reason, tc.verdict)
			}
		})
	}
}

func TestStatusShowsConditionsAndWhetherHoursHoldNow(t *testing.T) {
	env := testEnv(t, false)
	env.Location = time.UTC
	r := rules(t, clientHours(), domain.RuleSpec{Name: "nights", Action: "continue", Require: "ask",
		When: map[string]string{"hours": "22:00-06:00"}})
	st, err := RuleStatuses(r, env, map[string]bool{HarnessClaudeCode: true})
	if err != nil {
		t.Fatal(err)
	}
	when := st.Rules[0].When
	if len(when) != 2 || when[0].Condition != "folder /w/acme" || when[0].HoldsNow != nil {
		t.Fatalf("folder condition = %+v", when)
	}
	if when[1].Condition != "hours mon-fri 09:00-18:00" || when[1].HoldsNow == nil || !*when[1].HoldsNow {
		t.Errorf("hours condition = %+v; Monday 12:00 is inside", when[1])
	}
	if h := st.Rules[1].When[0]; h.HoldsNow == nil || *h.HoldsNow {
		t.Errorf("night condition = %+v; noon is outside", h)
	}
	for _, h := range st.Rules[0].Harnesses {
		if (h.Harness == HarnessCodex || h.Harness == HarnessCursor) && h.Enforcement != EnforcementNotEvaluated {
			t.Errorf("%s: %s; a folder rule cannot be judged without a working directory", h.Harness, h.Enforcement)
		}
	}
}

func TestAdviceJudgesHoursButNotFolders(t *testing.T) {
	env := testEnv(t, false)
	env.Location = time.UTC
	r := rules(t,
		domain.RuleSpec{Name: "office", Action: "continue", Require: "ask", Stage: "enforce", When: map[string]string{"hours": "09:00-18:00"}},
		domain.RuleSpec{Name: "nights", Action: "continue", Require: "ask", Stage: "enforce", When: map[string]string{"hours": "22:00-06:00"}},
		clientHours(),
	)
	got := AdviceNow(r, env, nil)
	if len(got) != 1 || got[0].Rule != "office" {
		t.Fatalf("advice = %+v; want only the office rule, which holds at noon", got)
	}
}
