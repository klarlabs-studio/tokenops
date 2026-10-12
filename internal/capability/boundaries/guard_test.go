package boundaries

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	domain "go.klarlabs.de/tokenops/internal/contexts/boundaries"
	"go.klarlabs.de/tokenops/internal/infra/boundaryledger"
	"go.klarlabs.de/tokenops/internal/infra/claudelimits"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

var testNow = time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)

func testEnv(t testing.TB, attended bool) Env {
	t.Helper()
	root := t.TempDir()
	return Env{
		Dir: boundaryledger.Dir(filepath.Join(root, "boundaries")), CoachDir: filepath.Join(root, "coach"),
		LimitsPath: filepath.Join(root, "claude-limits.json"), Now: func() time.Time { return testNow },
		User: "felix", Attended: attended,
	}
}

func setCost(t testing.TB, env Env, session string, usd float64) {
	t.Helper()
	if err := os.MkdirAll(env.CoachDir, 0o700); err != nil {
		t.Fatal(err)
	}
	body := fmt.Sprintf(`{"cumulative_usd":%v}`, usd)
	if err := os.WriteFile(filepath.Join(env.CoachDir, "session-"+session+".json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func rules(t *testing.T, specs ...domain.RuleSpec) []domain.Rule {
	t.Helper()
	r, err := domain.Parse(specs)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func spendRule(require, stage string) domain.RuleSpec {
	return domain.RuleSpec{Name: "session-spend", Action: "continue", Require: require, Stage: stage,
		When: map[string]string{"session_cost_usd": ">= 20"}}
}

func call(session string) Call {
	return Call{Harness: HarnessClaudeCode, SessionID: session, ToolUseID: "tu-1", Action: domain.ActionContinue}
}

func stages(t *testing.T, env Env, id string) []eventschema.DecisionStage {
	t.Helper()
	envs, err := env.Dir.Decision(id)
	if err != nil {
		t.Fatal(err)
	}
	out := make([]eventschema.DecisionStage, 0, len(envs))
	for _, e := range envs {
		out = append(out, e.Payload.(*eventschema.DecisionEvent).Stage)
	}
	return out
}

func TestGuardVerdicts(t *testing.T) {
	cases := []struct {
		name     string
		rule     domain.RuleSpec
		cost     float64
		attended bool
		verdict  string
		reason   []string
		recorded []eventschema.DecisionStage
	}{
		{"below the line", spendRule("ask", "enforce"), 5, false, VerdictNone, nil, nil},
		{"shadow records and allows", spendRule("deny", ""), 25, false, VerdictNone, nil,
			[]eventschema.DecisionStage{eventschema.DecisionStageShadow}},
		{"ask attended asks", spendRule("ask", "enforce"), 25, true, VerdictAsk,
			[]string{`"session-spend"`, "session cost $25.00 >= $20.00"}, []eventschema.DecisionStage{eventschema.DecisionStageProposed}},
		{"ask unattended denies with the way out", spendRule("ask", "enforce"), 25, false, VerdictDeny,
			[]string{"session cost $25.00 >= $20.00", "tokenops approve bnd-", "--session"}, []eventschema.DecisionStage{eventschema.DecisionStageProposed}},
		{"approver unattended names the role", spendRule("approver:admin", "enforce"), 25, false, VerdictDeny,
			[]string{"needs approval from admin", "tokenops approve"}, []eventschema.DecisionStage{eventschema.DecisionStageProposed}},
		{"deny refuses", spendRule("deny", "enforce"), 25, true, VerdictDeny,
			[]string{"refuses any further", "config.yaml"}, []eventschema.DecisionStage{eventschema.DecisionStageApplied}},
		{"allow says nothing", spendRule("allow", "enforce"), 25, false, VerdictNone, nil, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := testEnv(t, tc.attended)
			setCost(t, env, "s1", tc.cost)
			res, err := Guard(rules(t, tc.rule), call("s1"), env)
			if err != nil {
				t.Fatal(err)
			}
			if res.Verdict != tc.verdict {
				t.Fatalf("verdict = %q; want %q (%s)", res.Verdict, tc.verdict, res.Reason)
			}
			for _, want := range tc.reason {
				if !strings.Contains(res.Reason, want) {
					t.Errorf("reason %q lacks %q", res.Reason, want)
				}
			}
			got := stages(t, env, domain.DecisionID("session-spend", "s1"))
			if len(got) == 0 && len(tc.recorded) == 0 {
				return
			}
			if fmt.Sprint(got) != fmt.Sprint(tc.recorded) {
				t.Errorf("recorded %v; want %v", got, tc.recorded)
			}
		})
	}
}

func TestAMatchIsRecordedOncePerSessionAndCountedEveryCall(t *testing.T) {
	env := testEnv(t, false)
	setCost(t, env, "s1", 25)
	r := rules(t, spendRule("ask", ""))
	for range 3 {
		if _, err := Guard(r, call("s1"), env); err != nil {
			t.Fatal(err)
		}
	}
	if got := stages(t, env, domain.DecisionID("session-spend", "s1")); len(got) != 1 {
		t.Fatalf("recorded %v; want one shadow decision", got)
	}
	st, err := RuleStatuses(r, env, map[string]bool{HarnessClaudeCode: true})
	if err != nil {
		t.Fatal(err)
	}
	if st.Rules[0].Matches != 3 || st.Rules[0].Sessions != 1 {
		t.Fatalf("status = %+v", st.Rules[0])
	}
}

func TestApprovalsLetTheActionThrough(t *testing.T) {
	cases := []struct {
		name      string
		session   bool
		afterNext string
	}{
		{"once allows one step", false, VerdictDeny},
		{"session allows the rest", true, VerdictNone},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := testEnv(t, false)
			setCost(t, env, "s1", 25)
			r := rules(t, spendRule("ask", "enforce"))
			first, _ := Guard(r, call("s1"), env)
			if first.Verdict != VerdictDeny {
				t.Fatalf("first = %+v", first)
			}
			d, err := Decide(env, DecideRequest{ID: first.DecisionID, Decision: "approve", Session: tc.session})
			if err != nil {
				t.Fatal(err)
			}
			if d.Approver != "felix" || d.Rule != "session-spend" {
				t.Fatalf("decided = %+v", d)
			}
			if next, _ := Guard(r, call("s1"), env); next.Verdict != VerdictNone {
				t.Fatalf("after approval = %+v", next)
			}
			if after, _ := Guard(r, call("s1"), env); after.Verdict != tc.afterNext {
				t.Fatalf("after that = %+v; want %q", after, tc.afterNext)
			}
			envs, _ := env.Dir.Decision(first.DecisionID)
			var approved bool
			for _, e := range envs {
				p := e.Payload.(*eventschema.DecisionEvent)
				if p.Stage == eventschema.DecisionStageAccepted && p.Approval.Approver == "felix" && p.Approval.Method == MethodCLI {
					approved = true
				}
				if p.Actor.Harness != HarnessClaudeCode || p.Actor.SessionID != "s1" {
					t.Errorf("actor = %+v", p.Actor)
				}
			}
			if !approved {
				t.Fatal("no accepted decision carries the approval")
			}
		})
	}
}

func TestADeniedAnswerRefuses(t *testing.T) {
	env := testEnv(t, false)
	setCost(t, env, "s1", 25)
	r := rules(t, spendRule("ask", "enforce"))
	first, _ := Guard(r, call("s1"), env)
	if _, err := Decide(env, DecideRequest{ID: first.DecisionID, Decision: "deny", Session: true}); err != nil {
		t.Fatal(err)
	}
	res, _ := Guard(r, call("s1"), env)
	if res.Verdict != VerdictDeny || !strings.Contains(res.Reason, "felix declined") {
		t.Fatalf("res = %+v", res)
	}
}

func TestAnApprovalIsForItsSessionOnly(t *testing.T) {
	env := testEnv(t, false)
	setCost(t, env, "s1", 25)
	setCost(t, env, "s2", 25)
	r := rules(t, spendRule("ask", "enforce"))
	first, _ := Guard(r, call("s1"), env)
	if _, err := Decide(env, DecideRequest{ID: first.DecisionID, Decision: "approve", Session: true}); err != nil {
		t.Fatal(err)
	}
	if res, _ := Guard(r, call("s2"), env); res.Verdict != VerdictDeny {
		t.Fatalf("another session = %+v", res)
	}
}

func TestDecideRefusesWhatIsNotWaiting(t *testing.T) {
	env := testEnv(t, false)
	for _, req := range []DecideRequest{
		{ID: "bnd-000000000000", Decision: "approve"},
		{ID: "../etc", Decision: "approve"},
		{ID: domain.DecisionID("r", "s"), Decision: "maybe"},
	} {
		if _, err := Decide(env, req); err == nil {
			t.Errorf("Decide(%+v) accepted", req)
		}
	}
}

func TestAnAttendedAskIsSettledFromThePrompt(t *testing.T) {
	env := testEnv(t, true)
	setCost(t, env, "s1", 25)
	transcript := filepath.Join(t.TempDir(), "t.jsonl")
	r := rules(t, spendRule("ask", "enforce"))
	c := call("s1")
	c.TranscriptPath = transcript
	first, _ := Guard(r, c, env)
	if first.Verdict != VerdictAsk {
		t.Fatalf("first = %+v", first)
	}
	// The operator said yes: the asked call ran and has a result.
	if err := os.WriteFile(transcript, []byte(`{"message":{"content":[{"type":"tool_result","tool_use_id":"tu-1","content":"ok"}]}}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	c.ToolUseID = "tu-2"
	if next, _ := Guard(r, c, env); next.Verdict != VerdictNone {
		t.Fatalf("after yes at the prompt = %+v", next)
	}
	c.ToolUseID = "tu-3"
	if after, _ := Guard(r, c, env); after.Verdict != VerdictNone {
		t.Fatalf("the approval did not hold for the session: %+v", after)
	}
	envs, _ := env.Dir.Decision(first.DecisionID)
	last := envs[len(envs)-1].Payload.(*eventschema.DecisionEvent)
	if last.Approval.Method != MethodPrompt {
		t.Fatalf("approval = %+v", last.Approval)
	}
}

func TestPlanLeftReadsTheStatusLineReading(t *testing.T) {
	env := testEnv(t, false)
	r := rules(t, domain.RuleSpec{Name: "plan-floor", Action: "continue", Require: "deny", Stage: "enforce",
		When: map[string]string{"plan_left_pct": "< 5", "window": "week"}})
	write := func(observed time.Time, used float64) {
		t.Helper()
		if err := claudelimits.Write(env.LimitsPath, claudelimits.Reading{
			ObservedAt: observed, SevenDay: &claudelimits.Window{UsedPct: used, ResetsAt: testNow.Add(48 * time.Hour).Unix()},
		}); err != nil {
			t.Fatal(err)
		}
	}
	write(testNow.Add(-time.Minute), 97)
	if res, _ := Guard(r, call("s1"), env); res.Verdict != VerdictDeny || !strings.Contains(res.Reason, "plan left 3.0% < 5% (week)") {
		t.Fatalf("fresh reading = %+v", res)
	}
	write(testNow.Add(-2*time.Hour), 97)
	if res, _ := Guard(r, call("s2"), env); res.Verdict != VerdictNone {
		t.Fatalf("a stale reading refused: %+v", res)
	}
	opencode := call("s3")
	opencode.Harness = HarnessOpencode
	write(testNow.Add(-time.Minute), 97)
	if res, _ := Guard(r, opencode, env); res.Verdict != VerdictNone {
		t.Fatalf("Claude Code's reading applied to opencode: %+v", res)
	}
}

func TestSubagentModelRules(t *testing.T) {
	env := testEnv(t, false)
	r := rules(t, domain.RuleSpec{Name: "big-subagents", Action: "subagent_model", Require: "approver:admin", Stage: "enforce",
		When: map[string]string{"model_tier": "> sonnet"}})
	c := call("s1")
	c.Action = domain.ActionSubagentModel
	for model, want := range map[string]string{"opus": VerdictDeny, "claude-opus-4-7": VerdictDeny, "sonnet": VerdictNone, "": VerdictNone} {
		c.Model = model
		if res, _ := Guard(r, c, env); res.Verdict != want {
			t.Errorf("%q: verdict %q; want %q", model, res.Verdict, want)
		}
	}
	// A continue call never reads subagent rules.
	if res, _ := Guard(r, call("s1"), env); res.Verdict != VerdictNone {
		t.Fatal("subagent rule applied to continue")
	}
}

func TestNoRuleReadsNothing(t *testing.T) {
	env := testEnv(t, false)
	env.Dir = boundaryledger.Dir("/nonexistent/should/not/be/touched")
	if res, err := Guard(nil, call("s1"), env); err != nil || res.Verdict != VerdictNone {
		t.Fatalf("res = %+v, %v", res, err)
	}
}

func TestRecordFailureIsVisible(t *testing.T) {
	env := testEnv(t, false)
	RecordFailure(call("s1"), env, errors.New("disk full\nsecond line"))
	envs, err := env.Dir.Read()
	if err != nil || len(envs) != 1 {
		t.Fatalf("ledger = %d, %v", len(envs), err)
	}
	p := envs[0].Payload.(*eventschema.DecisionEvent)
	if p.Stage != eventschema.DecisionStageFailed || !strings.Contains(p.Rationale, "fail open") || strings.Contains(p.Rationale, "second line") {
		t.Fatalf("failure = %+v", p)
	}
}

func TestAttribution(t *testing.T) {
	agent, wf := Attribution(HarnessClaudeCode, "s1", TranscriptProject("/h/.claude/projects/-Users-x-repo/s1.jsonl"))
	if agent != "claude-code:-Users-x-repo" || wf != "claude-code:-Users-x-repo:s1" {
		t.Fatalf("attribution = %q %q", agent, wf)
	}
	agent, wf = Attribution(HarnessOpencode, "s1", "")
	if agent != "opencode" || wf != "opencode:s1" {
		t.Fatalf("attribution = %q %q", agent, wf)
	}
}

func TestStatusReportsEnforcementPerHarness(t *testing.T) {
	env := testEnv(t, false)
	r := rules(t, spendRule("approver:admin", "enforce"), domain.RuleSpec{Name: "shadowed", Action: "continue", Require: "deny"})
	st, err := RuleStatuses(r, env, map[string]bool{HarnessClaudeCode: true})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]map[string]string{
		"session-spend": {HarnessClaudeCode: EnforcementEnforced, HarnessOpencode: EnforcementNotInstalled, HarnessCodex: EnforcementAdviceOnly, HarnessCursor: EnforcementAdviceOnly},
		"shadowed":      {HarnessClaudeCode: EnforcementShadow},
	}
	for _, rs := range st.Rules {
		for _, h := range rs.Harnesses {
			if w, ok := want[rs.Name][h.Harness]; ok && w != h.Enforcement {
				t.Errorf("%s/%s = %q; want %q", rs.Name, h.Harness, h.Enforcement, w)
			}
		}
	}
	if !strings.Contains(st.Rules[0].Note, "answered on this machine") {
		t.Errorf("approver note = %q", st.Rules[0].Note)
	}
}

func TestStatusListsWhatWaits(t *testing.T) {
	env := testEnv(t, false)
	setCost(t, env, "s1", 25)
	r := rules(t, spendRule("ask", "enforce"))
	first, _ := Guard(r, call("s1"), env)
	st, _ := RuleStatuses(r, env, nil)
	if len(st.Pending) != 1 || st.Pending[0].ID != first.DecisionID || st.Pending[0].Approve != "tokenops approve "+first.DecisionID {
		t.Fatalf("pending = %+v", st.Pending)
	}
	_, _ = Decide(env, DecideRequest{ID: first.DecisionID, Decision: "approve"})
	if st, _ := RuleStatuses(r, env, nil); len(st.Pending) != 0 {
		t.Fatalf("answered decision still pending: %+v", st.Pending)
	}
}

func TestSetStagePromotesInConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	body := "boundaries:\n  - name: session-spend\n    action: continue\n    when: { session_cost_usd: \">= 20\" }\n    require: ask\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Promote(path, "session-spend"); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	if !strings.Contains(string(b), "stage: enforce") {
		t.Fatalf("config = %s", b)
	}
	if _, err := Promote(path, "nope"); err == nil {
		t.Fatal("promoted a rule that does not exist")
	}
}
