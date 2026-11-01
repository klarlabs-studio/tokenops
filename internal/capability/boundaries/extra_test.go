package boundaries

import (
	"strings"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/config"
	domain "go.klarlabs.de/tokenops/internal/contexts/boundaries"
	"go.klarlabs.de/tokenops/internal/contexts/boundaries/presence"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// enforcedPause is the headroom pause, promoted.
func enforcedPause() domain.RuleSpec {
	r := config.HeadroomPauseRule()
	r.Stage = string(domain.StageEnforce)
	return r
}

// A grant is the operator's word: only the CLI gives one, never while
// approvals must be signed, for a time in the future up to a day, and on
// local rules only. Unnamed, it lifts every local headroom rule.
func TestGrantExtraValidates(t *testing.T) {
	pause := enforcedPause()
	spend := spendRule("ask", "enforce")
	r := rules(t, pause, spend)
	org := r[1]
	org.Name, org.Source = "org-spend", domain.SourceOrg
	r = append(r, org)
	tests := []struct {
		name      string
		req       ExtraRequest
		approval  presence.Mode
		wantErr   string
		wantRules []string
		wantUntil time.Time
	}{
		{name: "over MCP", req: ExtraRequest{For: time.Hour, Method: MethodMCP}, wantErr: "operator's word"},
		{name: "over the API", req: ExtraRequest{For: time.Hour, Method: MethodAPI}, wantErr: "operator's word"},
		{name: "presence enforced", req: ExtraRequest{For: time.Hour, Method: MethodCLI}, approval: presence.ModePresence, wantErr: "cannot be signed"},
		{name: "no length", req: ExtraRequest{Method: MethodCLI}, wantErr: "how long"},
		{name: "both lengths", req: ExtraRequest{For: time.Hour, Until: testNow.Add(time.Hour), Method: MethodCLI}, wantErr: "not both"},
		{name: "in the past", req: ExtraRequest{Until: testNow.Add(-time.Minute), Method: MethodCLI}, wantErr: "not in the future"},
		{name: "over a day", req: ExtraRequest{For: 25 * time.Hour, Method: MethodCLI}, wantErr: "at most 24 hours"},
		{name: "unknown rule", req: ExtraRequest{For: time.Hour, Rules: []string{"nope"}, Method: MethodCLI}, wantErr: "no boundary is called"},
		{name: "organisation's rule", req: ExtraRequest{For: time.Hour, Rules: []string{"org-spend"}, Method: MethodCLI}, wantErr: "organisation"},
		{name: "default lifts the headroom rules", req: ExtraRequest{For: 30 * time.Minute, Method: MethodCLI, Why: "release day"},
			wantRules: []string{config.HeadroomPauseName}, wantUntil: testNow.Add(30 * time.Minute)},
		{name: "until a time, a named rule", req: ExtraRequest{Until: testNow.Add(6 * time.Hour), Rules: []string{"session-spend"}, Method: MethodCLI},
			wantRules: []string{"session-spend"}, wantUntil: testNow.Add(6 * time.Hour)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			env := testEnv(t, false)
			env.Approval = tc.approval
			got, err := GrantExtra(r, env, tc.req)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want %q", err, tc.wantErr)
				}
				if len(ActiveExtras(env)) != 0 {
					t.Fatal("a refused grant was recorded")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if strings.Join(got.Rules, ",") != strings.Join(tc.wantRules, ",") || !got.Until.Equal(tc.wantUntil) || got.Granter != "felix" {
				t.Fatalf("granted = %+v", got)
			}
			if active := ActiveExtras(env); len(active) != 1 || active[0].ID != got.ID {
				t.Fatalf("active = %+v", active)
			}
			// The grant is a decision, with who, why and until when.
			envs, err := env.Dir.Decision(got.ID)
			if err != nil || len(envs) != 1 {
				t.Fatalf("decisions = %v, %v", envs, err)
			}
			d := envs[0].Payload.(*eventschema.DecisionEvent)
			if d.Stage != eventschema.DecisionStageAccepted || d.Approval.Approver != "felix" || d.Approval.Method != MethodCLI ||
				!strings.Contains(d.Approval.Scope, tc.wantUntil.Format(time.RFC3339)) {
				t.Errorf("decision = %+v", d)
			}
			if tc.req.Why != "" && !strings.Contains(d.Rationale, tc.req.Why) {
				t.Errorf("rationale %q lacks the reason", d.Rationale)
			}
		})
	}
}

// While an extra allowance holds, the boundary it lifts lets every call
// through and records, once per session, that the extra did; another
// enforced rule still binds; once it ends, on its own or early, the
// boundary asks again.
func TestAnExtraAllowanceLiftsTheBoundaryUntilItEnds(t *testing.T) {
	env := testEnv(t, false)
	r := rules(t, enforcedPause(), spendRule("deny", "enforce"))
	writeLimits(t, env, 95)
	if res, err := Pause(r, call("s1"), env); err != nil || res.Verdict != VerdictDeny {
		t.Fatalf("before the extra: %+v, %v", res, err)
	}
	g, err := GrantExtra(r, env, ExtraRequest{For: 20 * time.Minute, Method: MethodCLI, Why: "demo"})
	if err != nil {
		t.Fatal(err)
	}
	for i := range 3 {
		res, err := Pause(r, call("s1"), env)
		if err != nil || res.Verdict != VerdictNone {
			t.Fatalf("call %d under the extra: %+v, %v", i, res, err)
		}
	}
	id := domain.DecisionID(config.HeadroomPauseName, "s1")
	envs, err := env.Dir.Decision(id)
	if err != nil {
		t.Fatal(err)
	}
	var lifted int
	for _, e := range envs {
		d := e.Payload.(*eventschema.DecisionEvent)
		if d.Approval.Method == MethodExtra {
			lifted++
			if d.Stage != eventschema.DecisionStageApplied || d.Approval.Approver != "felix" || !strings.Contains(d.Rationale, g.ID) ||
				!strings.Contains(d.Rationale, "demo") {
				t.Errorf("lifted decision = %+v", d)
			}
		}
	}
	if lifted != 1 {
		t.Errorf("%d lifted decisions recorded, want 1", lifted)
	}

	// Another enforced rule that matches still binds.
	setCost(t, env, "s2", 25)
	if res, _ := Pause(r, call("s2"), env); res.Verdict != VerdictDeny || !strings.Contains(res.Reason, "session-spend") {
		t.Errorf("the spend rule under the headroom extra: %+v", res)
	}

	// It ends on its own (the status line's reading is still fresh).
	later := env
	later.Now = func() time.Time { return testNow.Add(21 * time.Minute) }
	if res, _ := Pause(r, call("s3"), later); res.Verdict != VerdictDeny {
		t.Errorf("after the extra ended: %+v", res)
	}

	// Ended early, it lifts nothing.
	if _, err := GrantExtra(r, env, ExtraRequest{For: time.Hour, Method: MethodCLI}); err != nil {
		t.Fatal(err)
	}
	ended, err := EndExtras(env)
	if err != nil || len(ended) != 2 { // both are in force at testNow
		t.Fatalf("ended = %+v, %v", ended, err)
	}
	if res, _ := Pause(r, call("s4"), env); res.Verdict != VerdictDeny {
		t.Errorf("after ending the extra: %+v", res)
	}
	if stages := stages(t, env, ended[0].ID); stages[len(stages)-1] != eventschema.DecisionStageExpired {
		t.Errorf("an early end is not recorded: %v", stages)
	}
}

// Status shows the extra allowances in force, and none once they end.
func TestStatusShowsActiveExtras(t *testing.T) {
	env := testEnv(t, false)
	r := rules(t, enforcedPause())
	if _, err := GrantExtra(r, env, ExtraRequest{For: 2 * time.Hour, Method: MethodCLI, Why: "launch"}); err != nil {
		t.Fatal(err)
	}
	st, err := RuleStatuses(r, env, map[string]bool{})
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Extras) != 1 || st.Extras[0].Why != "launch" {
		t.Fatalf("extras = %+v", st.Extras)
	}
	env.Now = func() time.Time { return testNow.Add(3 * time.Hour) }
	if st, _ := RuleStatuses(r, env, map[string]bool{}); len(st.Extras) != 0 {
		t.Errorf("an ended extra is still shown: %+v", st.Extras)
	}
}
