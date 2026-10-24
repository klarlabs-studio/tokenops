package boundaries

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/boundaries/presence"
	"go.klarlabs.de/tokenops/internal/infra/boundaryledger"
	infrapresence "go.klarlabs.de/tokenops/internal/infra/presence"
	"go.klarlabs.de/tokenops/internal/infra/presence/presencetest"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

func TestMain(m *testing.M) {
	presencetest.MaybeRunHelper()
	os.Exit(m.Run())
}

// enforcedEnv is a ledger in a temp dir with boundary_approval set.
func enforcedEnv(t *testing.T, mode presence.Mode, attended bool) Env {
	t.Helper()
	env := testEnv(t, attended)
	env.Approval = mode
	return env
}

// fakeSigner installs the fake helper and returns it as a Signer.
func fakeSigner(t *testing.T) (Signer, *presencetest.Fake) {
	t.Helper()
	f := presencetest.Use(t)
	h, err := infrapresence.FindHelper()
	if err != nil {
		t.Fatal(err)
	}
	return h, f
}

// enrolFake enrols a key through the fake helper, an hour before the
// test's now so its pin predates every proof.
func enrolFake(t *testing.T, env Env, s Signer) Enrolled {
	t.Helper()
	early := env
	early.Now = func() time.Time { return testNow.Add(-time.Hour) }
	e, err := EnrollPresence(context.Background(), early, s, EnrollOptions{Machine: "test-mac"})
	if err != nil {
		t.Fatal(err)
	}
	return e
}

// raise makes the session-spend rule ask in session s1, unattended, and
// returns the decision id.
func raise(t *testing.T, env Env) string {
	t.Helper()
	setCost(t, env, "s1", 25)
	res, err := Guard(rules(t, spendRule("ask", "enforce")), call("s1"), env)
	if err != nil || res.DecisionID == "" {
		t.Fatalf("raise: %+v %v", res, err)
	}
	return res.DecisionID
}

func lastApproval(t *testing.T, env Env, id string) (eventschema.DecisionStage, eventschema.DecisionApproval, string) {
	t.Helper()
	envs, err := env.Dir.Decision(id)
	if err != nil || len(envs) == 0 {
		t.Fatalf("no decision %s: %v", id, err)
	}
	for i := len(envs) - 1; i >= 0; i-- {
		d := envs[i].Payload.(*eventschema.DecisionEvent)
		if d.Approval.Method != "" {
			return d.Stage, d.Approval, d.Rationale
		}
	}
	t.Fatalf("no approval recorded on %s", id)
	return "", eventschema.DecisionApproval{}, ""
}

// The whole opted-in path: enrol, sign through the helper, answer, and
// the pause point lets the next step through and records who approved,
// how, and with which key.
func TestEnforcedSignedApprovalIsHonoured(t *testing.T) {
	for _, session := range []bool{false, true} {
		env := enforcedEnv(t, presence.ModePresence, false)
		s, f := fakeSigner(t)
		enrolled := enrolFake(t, env, s)
		id := raise(t, env)
		proof, err := SignApproval(context.Background(), env, s, id, session)
		if err != nil {
			t.Fatal(err)
		}
		d, err := Decide(env, DecideRequest{ID: id, Decision: "approve", Session: session, Proof: proof, Method: MethodCLI})
		if err != nil {
			t.Fatal(err)
		}
		if d.Presence != PresenceSigned || d.PresenceKey != enrolled.KeyID || !strings.Contains(d.Note, "tokenops deny "+id) {
			t.Fatalf("decided %+v", d)
		}
		res, err := Guard(rules(t, spendRule("ask", "enforce")), call("s1"), env)
		if err != nil || res.Verdict != VerdictNone {
			t.Fatalf("session=%v: signed approval not honoured: %+v %v", session, res, err)
		}
		_, a, rationale := lastApproval(t, env, id)
		if a.Presence != PresenceSigned || a.PresenceKey != enrolled.KeyID || !strings.Contains(rationale, "approved in person") {
			t.Fatalf("recorded %+v %q", a, rationale)
		}
		// The dialog names the rule, the action and the session: that text
		// is what a paired Apple Watch shows.
		calls := f.Calls()
		dialog := calls[len(calls)-1]
		for _, want := range []string{`boundary "session-spend"`, "(continue)", "session s1", "session cost"} {
			if !strings.Contains(dialog, want) {
				t.Errorf("dialog %q lacks %q", dialog, want)
			}
		}
	}
}

// Validation step 3: `script -q /dev/null tokenops approve` gives the CLI a
// terminal, so it reaches Decide as a CLI approval, but without the
// helper's signature it is refused.
func TestEnforcedUnsignedCLIApprovalIsRefused(t *testing.T) {
	env := enforcedEnv(t, presence.ModePresence, false)
	s, _ := fakeSigner(t)
	enrolFake(t, env, s)
	id := raise(t, env)
	_, err := Decide(env, DecideRequest{ID: id, Decision: "approve", Method: MethodCLI})
	if err == nil || !strings.Contains(err.Error(), "carries no presence signature") || !strings.Contains(err.Error(), "tokenops approve "+id) {
		t.Fatalf("unsigned approval: %v", err)
	}
	if _, ok, _ := env.Dir.GetAnswer(id, env.now()); ok {
		t.Fatal("a refused approval wrote an answer")
	}
	// Denying needs no signature.
	if _, err := Decide(env, DecideRequest{ID: id, Decision: "deny", Method: MethodCLI}); err != nil {
		t.Fatalf("deny: %v", err)
	}
}

// Validation step 3: the daemon's endpoint takes an approval only with a
// valid proof; MCP never approves, proof or not.
func TestAPIApprovesOnlyWithAProof(t *testing.T) {
	for _, mode := range []presence.Mode{presence.ModeChannel, presence.ModePresence} {
		env := enforcedEnv(t, mode, false)
		s, _ := fakeSigner(t)
		enrolFake(t, env, s)
		id := raise(t, env)
		if _, err := Decide(env, DecideRequest{ID: id, Decision: "approve", Method: MethodAPI}); err == nil {
			t.Fatalf("%s: unsigned API approval accepted", mode)
		}
		proof, err := SignApproval(context.Background(), env, s, id, false)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := Decide(env, DecideRequest{ID: id, Decision: "approve", Proof: proof, Method: MethodMCP}); err == nil {
			t.Fatalf("%s: MCP approved with a proof", mode)
		}
		forged := *proof
		forged.Session = "s2"
		if _, err := Decide(env, DecideRequest{ID: id, Decision: "approve", Proof: &forged, Method: MethodAPI}); err == nil ||
			!strings.Contains(err.Error(), "another session") {
			t.Fatalf("%s: altered proof: %v", mode, err)
		}
		d, err := Decide(env, DecideRequest{ID: id, Decision: "approve", Proof: proof, Method: MethodAPI})
		if err != nil || d.Presence != PresenceSigned {
			t.Fatalf("%s: signed API approval: %+v %v", mode, d, err)
		}
	}
}

// Validation step 3: an answer file written by hand is not honoured under
// enforcement, is recorded as refused with the reason, and is spent so it
// is recorded once; the refusal tells the operator why.
func TestEnforcedHandWrittenAnswerIsNotHonoured(t *testing.T) {
	env := enforcedEnv(t, presence.ModePresence, false)
	s, _ := fakeSigner(t)
	enrolFake(t, env, s)
	id := raise(t, env)
	if err := env.Dir.PutAnswer(boundaryledger.Answer{
		ID: id, Rule: "session-spend", SessionID: "s1", Approved: true, Scope: boundaryledger.ScopeSession,
		Approver: "felix", Method: MethodCLI, At: testNow,
	}); err != nil {
		t.Fatal(err)
	}
	r := rules(t, spendRule("ask", "enforce"))
	res, err := Guard(r, call("s1"), env)
	if err != nil || res.Verdict != VerdictDeny || !strings.Contains(res.Reason, "was given but not honoured") ||
		!strings.Contains(res.Reason, "no presence signature") {
		t.Fatalf("hand-written answer: %+v %v", res, err)
	}
	stage, a, rationale := lastApproval(t, env, id)
	if stage != eventschema.DecisionStageProposed || a.Presence != PresenceRefused || a.WouldRefuse != string(presence.ReasonUnsigned) ||
		!strings.Contains(rationale, "unsigned_answer_ignored") {
		t.Fatalf("recorded %s %+v %q", stage, a, rationale)
	}
	if _, ok, _ := env.Dir.GetAnswer(id, env.now()); ok {
		t.Fatal("the refused answer was kept")
	}
	if res, _ := Guard(r, call("s1"), env); res.Verdict != VerdictDeny || strings.Contains(res.Reason, "not honoured") {
		t.Fatalf("next call: %+v", res)
	}
	st := Presence(env)
	if st.State != PresenceStateEnforced || st.Refused != 1 || !strings.Contains(st.Note, "1 approval(s) were not honoured") {
		t.Fatalf("status %+v", st)
	}
}

// Validation step 3: a public key replaced without the old key's signature
// holds approvals for 15 minutes and is announced; after the hold, an
// approval signed under the new key counts.
func TestEnforcedReplacedKeyHoldsApprovals(t *testing.T) {
	env := enforcedEnv(t, presence.ModePresence, false)
	s, _ := fakeSigner(t)
	enrolFake(t, env, s)
	if _, err := EnrollPresence(context.Background(), env, s, EnrollOptions{}); err == nil {
		t.Fatal("a second enrolment without --replace was accepted")
	}
	replaced, err := EnrollPresence(context.Background(), env, s, EnrollOptions{Replace: true})
	if err != nil || replaced.HoldTill == nil || !strings.Contains(replaced.Note, "held until") {
		t.Fatalf("replace: %+v %v", replaced, err)
	}
	id := raise(t, env)
	res, _ := Guard(rules(t, spendRule("ask", "enforce")), call("s1"), env)
	if !strings.Contains(res.Reason, "Approvals are held until") {
		t.Fatalf("refusal during the hold: %q", res.Reason)
	}
	if _, err := SignApproval(context.Background(), env, s, id, false); !errors.Is(err, ErrPresenceUnavailable) {
		t.Fatalf("signing during the hold: %v", err)
	}
	st := Presence(env)
	if st.State != PresenceStateHeld || !strings.Contains(st.Note, "approval key replaced at") || !strings.Contains(st.Note, "refused until then") {
		t.Fatalf("status %+v", st)
	}
	later := env
	later.Now = func() time.Time { return testNow.Add(presence.ReplacementHold + time.Minute) }
	proof, err := SignApproval(context.Background(), later, s, id, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Decide(later, DecideRequest{ID: id, Decision: "approve", Proof: proof, Method: MethodCLI}); err != nil {
		t.Fatal(err)
	}
	if res, _ := Guard(rules(t, spendRule("ask", "enforce")), call("s1"), later); res.Verdict != VerdictNone {
		t.Fatalf("after the hold: %+v", res)
	}
}

// A rotation the old key signed holds nothing, and a proof under the old
// key no longer counts.
func TestRotationMovesTheKeyWithoutAHold(t *testing.T) {
	env := enforcedEnv(t, presence.ModePresence, false)
	s, f := fakeSigner(t)
	first := enrolFake(t, env, s)
	id := raise(t, env)
	old, err := SignApproval(context.Background(), env, s, id, false)
	if err != nil {
		t.Fatal(err)
	}
	rotated, err := RotatePresence(context.Background(), env, s, "test-mac")
	if err != nil || rotated.How != presence.PinRotated || rotated.Previous != first.KeyID || rotated.HoldTill != nil {
		t.Fatalf("rotate: %+v %v", rotated, err)
	}
	if _, err := Decide(env, DecideRequest{ID: id, Decision: "approve", Proof: old, Method: MethodCLI}); err == nil {
		t.Fatal("a proof under the rotated-out key was accepted")
	}
	proof, err := SignApproval(context.Background(), env, s, id, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Decide(env, DecideRequest{ID: id, Decision: "approve", Proof: proof, Method: MethodCLI}); err != nil {
		t.Fatal(err)
	}
	var rotateDialog bool
	for _, c := range f.Calls() {
		rotateDialog = rotateDialog || strings.Contains(c, "rotate this Mac's TokenOps approval key from "+first.KeyID)
	}
	if !rotateDialog {
		t.Fatalf("no rotation dialog in %q", f.Calls())
	}
	envs, _ := env.Dir.Decision("presence:" + rotated.KeyID)
	if len(envs) != 1 {
		t.Fatalf("rotation recorded %d times", len(envs))
	}
}

// Opted in with no key: approvals are refused with the enrol command, the
// refusal the agent reads says so, and deny still works.
func TestEnforcedWithoutAKey(t *testing.T) {
	env := enforcedEnv(t, presence.ModePresence, false)
	id := raise(t, env)
	res, _ := Guard(rules(t, spendRule("ask", "enforce")), call("s1"), env)
	if !strings.Contains(res.Reason, EnrolCommand) || !strings.Contains(res.Reason, "boundaries demote session-spend") {
		t.Fatalf("refusal %q", res.Reason)
	}
	if _, err := Decide(env, DecideRequest{ID: id, Decision: "approve", Method: MethodCLI}); err == nil ||
		!strings.Contains(err.Error(), EnrolCommand) {
		t.Fatalf("approve: %v", err)
	}
	s, _ := fakeSigner(t)
	if _, err := SignApproval(context.Background(), env, s, id, false); !errors.Is(err, ErrPresenceUnavailable) {
		t.Fatalf("sign: %v", err)
	}
	if _, err := Decide(env, DecideRequest{ID: id, Decision: "deny", Method: MethodMCP}); err != nil {
		t.Fatalf("deny: %v", err)
	}
	if st := Presence(env); st.State != PresenceStateUnavailable || st.Enrol != EnrolCommand {
		t.Fatalf("status %+v", st)
	}
}

// presence keeps Claude Code's prompt (harness-attested); presence-strict
// drops it: the pause point does not ask there, and a prompt answer on
// disk is not honoured.
func TestStrictDropsThePrompt(t *testing.T) {
	for _, tc := range []struct {
		mode     presence.Mode
		verdict  string
		honoured bool
	}{
		{presence.ModePresence, VerdictAsk, true},
		{presence.ModeStrict, VerdictDeny, false},
	} {
		env := enforcedEnv(t, tc.mode, true)
		s, _ := fakeSigner(t)
		enrolFake(t, env, s)
		setCost(t, env, "s1", 25)
		r := rules(t, spendRule("ask", "enforce"))
		res, _ := Guard(r, call("s1"), env)
		if res.Verdict != tc.verdict {
			t.Fatalf("%s: attended verdict %q, want %q", tc.mode, res.Verdict, tc.verdict)
		}
		if err := env.Dir.PutAnswer(boundaryledger.Answer{
			ID: res.DecisionID, Rule: "session-spend", SessionID: "s1", Approved: true, Scope: boundaryledger.ScopeSession,
			Approver: "felix", Method: MethodPrompt, At: testNow,
		}); err != nil {
			t.Fatal(err)
		}
		res, _ = Guard(r, call("s1"), env)
		if (res.Verdict == VerdictNone) != tc.honoured {
			t.Fatalf("%s: prompt answer -> %+v", tc.mode, res)
		}
		if !tc.honoured && !strings.Contains(res.Reason, "presence-strict") {
			t.Fatalf("%s: reason %q", tc.mode, res.Reason)
		}
	}
}

// Under channel with a key enrolled, an approval with a proof is recorded
// as signed and one without goes through as before (shadow).
func TestChannelWithKeyRecordsSignedApprovals(t *testing.T) {
	env := enforcedEnv(t, presence.ModeChannel, false)
	s, _ := fakeSigner(t)
	enrolFake(t, env, s)
	id := raise(t, env)
	if _, err := Decide(env, DecideRequest{ID: id, Decision: "approve", Method: MethodCLI}); err != nil {
		t.Fatalf("unsigned approval under channel: %v", err)
	}
	if res, _ := Guard(rules(t, spendRule("ask", "enforce")), call("s1"), env); res.Verdict != VerdictNone {
		t.Fatalf("%+v", res)
	}
	if st := Presence(env); st.State != PresenceStateShadow || !strings.Contains(st.Note, "boundary_approval: presence enforces it") {
		t.Fatalf("status %+v", st)
	}
}

// A person cancelling the dialog is not "unavailable": the CLI stops
// rather than going on unsigned.
func TestCancelledDialogIsNotUnavailable(t *testing.T) {
	env := enforcedEnv(t, presence.ModeChannel, false)
	s, f := fakeSigner(t)
	enrolFake(t, env, s)
	id := raise(t, env)
	f.Fail(presencetest.ExitCancelled)
	_, err := SignApproval(context.Background(), env, s, id, false)
	if !errors.Is(err, infrapresence.ErrCancelled) || errors.Is(err, ErrPresenceUnavailable) {
		t.Fatalf("cancel: %v", err)
	}
	f.Fail(presencetest.ExitNoGUI)
	if _, err := SignApproval(context.Background(), env, s, id, false); !errors.Is(err, ErrPresenceUnavailable) {
		t.Fatalf("no GUI: %v", err)
	}
}
