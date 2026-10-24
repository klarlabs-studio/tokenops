package boundaries

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	domain "go.klarlabs.de/tokenops/internal/contexts/boundaries"
	"go.klarlabs.de/tokenops/internal/contexts/boundaries/presence"
	"go.klarlabs.de/tokenops/internal/infra/boundaryledger"
	infrapresence "go.klarlabs.de/tokenops/internal/infra/presence"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// enrol writes a software P-256 public key where the presence key store
// reads it, standing in for phase 4's enrolment. No dialog is raised.
func enrol(t testing.TB, env Env) *ecdsa.PrivateKey {
	t.Helper()
	return enrolAt(t, env, env.now().Add(-time.Hour))
}

// enrolAt writes a key and lets the store first see it at at.
func enrolAt(t testing.TB, env Env, at time.Time) *ecdsa.PrivateKey {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := priv.PublicKey.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	dir := string(env.presenceDir())
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, infrapresence.PublicKeyFile), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := env.presenceDir().Trust(at); err != nil {
		t.Fatal(err)
	}
	return priv
}

// signAnswer adds a presence proof to the answer waiting for id, the way
// phase 4's `tokenops approve` will: over the pending decision's challenge
// and the crossings the answer records.
func signAnswer(t testing.TB, env Env, priv *ecdsa.PrivateKey, id string) boundaryledger.Answer {
	t.Helper()
	a, ok, err := env.Dir.GetAnswer(id, env.now())
	if err != nil || !ok {
		t.Fatalf("no answer for %s: %v", id, err)
	}
	p, _, _ := env.Dir.GetPending(id)
	raw, _ := priv.PublicKey.Bytes()
	proof := presence.Proof{
		V: presence.Version, Key: presence.KeyID(raw), Decision: id, Rule: a.Rule, Session: a.SessionID,
		Scope: a.Scope, Challenge: p.Challenge, Crossed: presence.CrossingsDigest(a.Crossings),
		Issued: env.now().Truncate(time.Second),
	}
	msg, err := proof.Message()
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(msg)
	if proof.Sig, err = ecdsa.SignASN1(rand.Reader, priv, sum[:]); err != nil {
		t.Fatal(err)
	}
	a.Proof = &proof
	if err := env.Dir.PutAnswer(a); err != nil {
		t.Fatal(err)
	}
	return a
}

// appliedApproval is the approval on the decision's last applied stage.
func appliedApproval(t testing.TB, env Env, id string) (eventschema.DecisionApproval, string) {
	t.Helper()
	envs, err := env.Dir.Decision(id)
	if err != nil {
		t.Fatal(err)
	}
	for i := len(envs) - 1; i >= 0; i-- {
		d := envs[i].Payload.(*eventschema.DecisionEvent)
		if d.Stage == eventschema.DecisionStageApplied && d.Approval.Approver != "" {
			return d.Approval, d.Rationale
		}
	}
	t.Fatal("no applied approval recorded")
	return eventschema.DecisionApproval{}, ""
}

// askAndApprove raises the session-spend ask and answers it at the CLI.
func askAndApprove(t *testing.T, env Env, session bool) (string, []domain.Rule) {
	t.Helper()
	setCost(t, env, "s1", 25)
	r := rules(t, spendRule("ask", "enforce"))
	first, err := Guard(r, call("s1"), env)
	if err != nil || first.Verdict != VerdictDeny {
		t.Fatalf("first = %+v %v", first, err)
	}
	if _, err := Decide(env, DecideRequest{ID: first.DecisionID, Decision: "approve", Session: session}); err != nil {
		t.Fatal(err)
	}
	return first.DecisionID, r
}

// Every machine today: no key, so approvals are recorded as not_enrolled,
// with no line on any decision, and status says it once.
func TestPresenceNotEnrolledIsQuiet(t *testing.T) {
	env := testEnv(t, false)
	id, r := askAndApprove(t, env, false)
	if res, err := Guard(r, call("s1"), env); err != nil || res.Verdict != VerdictNone {
		t.Fatalf("approval not honoured: %+v %v", res, err)
	}
	a, rationale := appliedApproval(t, env, id)
	if a.Presence != PresenceNotEnrolled || a.WouldRefuse != "" || strings.Contains(rationale, "presence") {
		t.Fatalf("approval = %+v; rationale %q", a, rationale)
	}
	if _, err := os.Stat(string(env.presenceDir())); !os.IsNotExist(err) {
		t.Fatalf("the check created the key store: %v", err)
	}
	st, err := RuleStatuses(r, env, map[string]bool{HarnessClaudeCode: true})
	if err != nil {
		t.Fatal(err)
	}
	if st.Presence == nil || st.Presence.State != PresenceStateNotEnrolled ||
		!strings.Contains(st.Presence.Note, "presence approval not enrolled") || st.Presence.WouldBeIgnored != 0 {
		t.Fatalf("status presence = %+v", st.Presence)
	}
	if empty, _ := RuleStatuses(nil, env, nil); empty.Presence != nil {
		t.Fatalf("no rules, yet presence reported: %+v", empty.Presence)
	}
}

// With a key enrolled, an unsigned approval is still honoured (shadow),
// and the decision records that enforcement would not have honoured it.
func TestPresenceShadowRecordsAnUnsignedApproval(t *testing.T) {
	env := testEnv(t, false)
	enrol(t, env)
	id, r := askAndApprove(t, env, false)
	if res, _ := Guard(r, call("s1"), env); res.Verdict != VerdictNone {
		t.Fatalf("shadow refused an approval: %+v", res)
	}
	a, rationale := appliedApproval(t, env, id)
	if a.Presence != PresenceUnverified || a.WouldRefuse != string(presence.ReasonUnsigned) || a.Method != MethodCLI {
		t.Fatalf("approval = %+v", a)
	}
	if !strings.Contains(rationale, "presence check in shadow") || !strings.Contains(rationale, "no presence signature") {
		t.Fatalf("rationale = %q", rationale)
	}
	st, err := RuleStatuses(r, env, map[string]bool{HarnessClaudeCode: true})
	if err != nil {
		t.Fatal(err)
	}
	if st.Presence == nil || st.Presence.State != PresenceStateShadow || st.Presence.WouldBeIgnored != 1 ||
		!strings.Contains(st.Presence.Note, "1 approval(s) would not have been honoured") {
		t.Fatalf("status presence = %+v", st.Presence)
	}
}

func TestPresenceShadowVerifiesASignedApproval(t *testing.T) {
	for _, session := range []bool{false, true} {
		env := testEnv(t, false)
		priv := enrol(t, env)
		id, r := askAndApprove(t, env, session)
		signAnswer(t, env, priv, id)
		if res, _ := Guard(r, call("s1"), env); res.Verdict != VerdictNone {
			t.Fatalf("session=%v: %+v", session, res)
		}
		a, rationale := appliedApproval(t, env, id)
		raw, _ := priv.PublicKey.Bytes()
		if a.Presence != PresenceSigned || a.PresenceKey != presence.KeyID(raw) || a.WouldRefuse != "" ||
			strings.Contains(rationale, "presence") {
			t.Fatalf("session=%v: approval = %+v; rationale %q", session, a, rationale)
		}
	}
}

// A one-step signed approval answers one raising: once used, the pending
// decision's challenge is replaced and the proof is remembered, so the
// same answer written back is not a valid approval.
func TestPresenceOneStepProofIsSpentWhenUsed(t *testing.T) {
	env := testEnv(t, false)
	priv := enrol(t, env)
	id, r := askAndApprove(t, env, false)
	before, _, _ := env.Dir.GetPending(id)
	signed := signAnswer(t, env, priv, id)
	if res, _ := Guard(r, call("s1"), env); res.Verdict != VerdictNone {
		t.Fatalf("%+v", res)
	}
	after, _, _ := env.Dir.GetPending(id)
	if after.Challenge == "" || after.Challenge == before.Challenge || !slices.Contains(after.Spent, signed.Proof.Fingerprint()) {
		t.Fatalf("challenge %q → %q, spent %v", before.Challenge, after.Challenge, after.Spent)
	}
	// Replay: the consumed answer, proof and all, written back.
	if err := env.Dir.PutAnswer(signed); err != nil {
		t.Fatal(err)
	}
	if res, _ := Guard(r, call("s1"), env); res.Verdict != VerdictNone {
		t.Fatalf("shadow refused the replay: %+v", res)
	}
	a, _ := appliedApproval(t, env, id)
	if a.Presence != PresenceUnverified || a.WouldRefuse != string(presence.ReasonChallenge) {
		t.Fatalf("replayed approval = %+v", a)
	}
}

// The challenge is minted when a decision is raised and kept while it
// waits, so an approval signed for it still names it on the next call.
func TestPresenceChallengeIsKeptWhileTheDecisionWaits(t *testing.T) {
	env := testEnv(t, false)
	setCost(t, env, "s1", 25)
	r := rules(t, spendRule("ask", "enforce"))
	first, _ := Guard(r, call("s1"), env)
	p1, _, _ := env.Dir.GetPending(first.DecisionID)
	setCost(t, env, "s1", 30)
	if _, err := Guard(r, call("s1"), env); err != nil {
		t.Fatal(err)
	}
	p2, _, _ := env.Dir.GetPending(first.DecisionID)
	if len(p1.Challenge) != 32 || p1.Challenge != p2.Challenge {
		t.Fatalf("challenge %q then %q", p1.Challenge, p2.Challenge)
	}
}

// A key replaced without the old key's signature holds approvals and is
// announced in status.
func TestPresenceReplacedKeyIsHeldAndAnnounced(t *testing.T) {
	env := testEnv(t, false)
	enrol(t, env)
	priv := enrolAt(t, env, env.now()) // a second key, unsigned by the first
	id, r := askAndApprove(t, env, false)
	signAnswer(t, env, priv, id)
	st, err := RuleStatuses(r, env, map[string]bool{HarnessClaudeCode: true})
	if err != nil {
		t.Fatal(err)
	}
	if st.Presence == nil || st.Presence.State != PresenceStateHeld || st.Presence.HoldUntil == nil ||
		!strings.Contains(st.Presence.Note, "approval key replaced at") {
		t.Fatalf("status presence = %+v", st.Presence)
	}
	if res, _ := Guard(r, call("s1"), env); res.Verdict != VerdictNone {
		t.Fatalf("%+v", res)
	}
	if a, _ := appliedApproval(t, env, id); a.WouldRefuse != string(presence.ReasonKeyHeld) {
		t.Fatalf("approval = %+v", a)
	}
}
