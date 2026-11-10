package boundaries

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	domain "go.klarlabs.de/tokenops/internal/contexts/boundaries"
	"go.klarlabs.de/tokenops/internal/contexts/boundaries/presence"
	"go.klarlabs.de/tokenops/internal/infra/boundaryledger"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// unwritable replaces the ledger directory with a file, so every write to
// it fails while the rules and the session's cost stay readable.
func unwritable(t *testing.T, env Env) {
	t.Helper()
	if err := os.RemoveAll(env.Dir.Root()); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(env.Dir.Root(), []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
}

// readOnly takes write permission from one of the ledger's directories
// for the length of the test.
func readOnly(t *testing.T, dir string) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("root writes to a read-only directory")
	}
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) }) //nolint:gosec // restores the test's own directory
}

// A rule that matched has decided. Failing to write that down is a
// failure to report, not a reason to let the call through.
func TestADecidedVerdictStandsWhenTheLedgerCannotBeWritten(t *testing.T) {
	cases := []struct {
		name     string
		rule     domain.RuleSpec
		attended bool
		verdict  string
	}{
		{"deny", spendRule("deny", "enforce"), false, VerdictDeny},
		{"ask, attended", spendRule("ask", "enforce"), true, VerdictAsk},
		{"ask, unattended", spendRule("ask", "enforce"), false, VerdictDeny},
		{"approver", spendRule("approver:admin", "enforce"), false, VerdictDeny},
		{"shadow binds nothing", spendRule("deny", ""), false, VerdictNone},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := testEnv(t, tc.attended)
			setCost(t, env, "s1", 25)
			unwritable(t, env)
			res, err := Guard(rules(t, tc.rule), call("s1"), env)
			if err == nil {
				t.Fatal("the failed write was not reported")
			}
			if res.Verdict != tc.verdict {
				t.Fatalf("verdict = %q; want %q", res.Verdict, tc.verdict)
			}
			if tc.verdict != VerdictNone && (res.Reason == "" || res.DecisionID == "") {
				t.Errorf("verdict without its reason or id: %+v", res)
			}
		})
	}
}

// The pause point checks continue and spend: a failure while checking one
// must not drop what the other decided.
func TestPauseKeepsTheStrictestVerdictThroughAFailure(t *testing.T) {
	env := testEnv(t, false)
	setCost(t, env, "s1", 25)
	unwritable(t, env)
	res, err := Pause(rules(t, spendRule("deny", "enforce")), call("s1"), env)
	if err == nil || res.Verdict != VerdictDeny {
		t.Fatalf("res = %+v, %v; want the deny and the failure", res, err)
	}
}

// An answer that cannot be read is no answer: the rule still asks.
func TestAnUnreadableAnswerIsNoAnswer(t *testing.T) {
	env := testEnv(t, false)
	setCost(t, env, "s1", 25)
	rs := rules(t, spendRule("ask", "enforce"))
	id := domain.DecisionID("session-spend", "s1")
	answers := filepath.Join(env.Dir.Root(), "answers")
	if err := os.MkdirAll(answers, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(answers, id+".json"), []byte(`{"approved": tr`), 0o600); err != nil {
		t.Fatal(err)
	}
	res, err := Guard(rs, call("s1"), env)
	if err == nil {
		t.Error("the unreadable answer was not reported")
	}
	if res.Verdict != VerdictDeny {
		t.Fatalf("verdict = %q; want the rule to go on asking", res.Verdict)
	}
	if _, waiting, _ := env.Dir.GetPending(id); !waiting {
		t.Error("the decision is not waiting, so nobody can answer it")
	}
}

// An approval that cannot be spent, or cannot be set aside, is not an
// approval: otherwise a one-step answer would hold for every later call.
func TestAnAnswerThatCannotBeSpentIsNotHonoured(t *testing.T) {
	cases := []struct {
		name     string
		approval presence.Mode
		method   string
	}{
		{"one step, by the channel", presence.ModeChannel, MethodCLI},
		{"unsigned, under presence", presence.ModePresence, MethodCLI},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := testEnv(t, false)
			env.Approval = tc.approval
			if tc.approval.Enforced() {
				s, _ := fakeSigner(t)
				enrolFake(t, env, s)
			}
			setCost(t, env, "s1", 25)
			rs := rules(t, spendRule("ask", "enforce"))
			id := domain.DecisionID("session-spend", "s1")
			if res, err := Guard(rs, call("s1"), env); err != nil || res.Verdict != VerdictDeny {
				t.Fatalf("raising: %+v, %v", res, err)
			}
			answer := boundaryledger.Answer{
				ID: id, Rule: "session-spend", SessionID: "s1", Approved: true,
				Scope: boundaryledger.ScopeOnce, Approver: "felix", Method: tc.method, At: testNow,
			}
			if err := env.Dir.PutAnswer(answer); err != nil {
				t.Fatal(err)
			}
			readOnly(t, filepath.Join(env.Dir.Root(), "answers"))
			for i := range 2 {
				res, err := Guard(rs, call("s1"), env)
				if err == nil {
					t.Errorf("call %d: the failure was not reported", i+1)
				}
				if res.Verdict != VerdictDeny {
					t.Fatalf("call %d: verdict = %q; want the refusal to stand", i+1, res.Verdict)
				}
			}
		})
	}
}

// The router and the uploader ask the same guard: a rule that held a
// route, or figures, holds them whether or not the ledger took the record.
func TestTheOtherPointsHoldThroughAFailedRecord(t *testing.T) {
	t.Run("route", func(t *testing.T) {
		env := testEnv(t, false)
		unwritable(t, env)
		r := rules(t, domain.RuleSpec{
			Name: "no-opus-routes", Action: "model_route", Require: "deny", Stage: "enforce",
			When: map[string]string{"model_tier": ">= opus"},
		})
		held, reason := RouteCheck(func() []Rule { return r }, env)(eventschema.ProviderAnthropic, "claude-sonnet-4-5", "claude-opus-4-1", "", "")
		if !held || reason == "" {
			t.Fatalf("held %v, reason %q", held, reason)
		}
	})
	t.Run("share", func(t *testing.T) {
		env := testEnv(t, false)
		unwritable(t, env)
		r := rules(t, domain.RuleSpec{
			Name: "nothing-to-team", Action: "share", Require: "deny", Stage: "enforce",
			When: map[string]string{"destination": "== team"},
		})
		if err := CheckShare(r, domain.DestinationTeam, env); !IsShareRefused(err) {
			t.Fatalf("err = %v; want the figures held", err)
		}
	})
}

func TestAFailureSaysWhetherTheCallWasHeld(t *testing.T) {
	cause := errors.New("disk full\nsecond line")
	cases := []struct {
		name string
		res  Result
		want string
		not  string
	}{
		{"let through", Result{}, "fail open", "held"},
		{"held", Result{Verdict: VerdictDeny, DecisionID: "bnd-1"}, "held the call (deny)", "fail open"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := testEnv(t, false)
			RecordFailure(call("s1"), env, tc.res, cause)
			envs, err := env.Dir.Read()
			if err != nil || len(envs) != 1 {
				t.Fatalf("ledger = %d, %v", len(envs), err)
			}
			got := envs[0].Payload.(*eventschema.DecisionEvent).Rationale
			if !strings.Contains(got, tc.want) || strings.Contains(got, tc.not) || strings.Contains(got, "second line") {
				t.Errorf("rationale = %q", got)
			}
			note := FailureNote(tc.res, cause)
			if !strings.Contains(note, "disk full") || strings.Contains(note, "second line") {
				t.Errorf("note = %q", note)
			}
		})
	}
	if FailureNote(Result{Verdict: VerdictDeny}, nil) != "" {
		t.Error("a note with nothing failed")
	}
}

// A failed check is written to the ledger; status is where it is read.
func TestStatusCountsTheLastDaysFailedChecks(t *testing.T) {
	env := testEnv(t, false)
	r := rules(t, domain.RuleSpec{Name: "session-spend", Action: "continue", Require: "ask",
		When: map[string]string{"session_cost_usd": ">= 20"}})
	if st, err := RuleStatuses(r, env, nil); err != nil || st.Failed != nil {
		t.Fatalf("failed = %+v, %v; want none", st.Failed, err)
	}
	at := func(ago time.Duration) Env {
		e := env
		e.Now = func() time.Time { return testNow.Add(-ago) }
		return e
	}
	RecordFailure(call("s1"), at(30*time.Hour), Result{}, errors.New("too old to count"))
	RecordFailure(call("s1"), at(2*time.Hour), Result{}, errors.New("config unreadable"))
	RecordFailure(call("s2"), at(time.Hour), Result{Verdict: VerdictDeny}, errors.New("disk full"))
	st, err := RuleStatuses(r, env, nil)
	if err != nil {
		t.Fatal(err)
	}
	f := st.Failed
	if f == nil || f.Count != 2 || !f.LastAt.Equal(testNow.Add(-time.Hour)) ||
		!strings.Contains(f.Last, "held the call (deny)") || !strings.Contains(f.Last, "disk full") {
		t.Fatalf("failed = %+v", f)
	}
}
