package presence

import (
	"strings"
	"testing"
	"time"
)

func TestParseMode(t *testing.T) {
	for _, tc := range []struct {
		in       string
		want     Mode
		enforced bool
		strict   bool
		err      bool
	}{
		{in: "", want: ModeChannel},
		{in: "channel", want: ModeChannel},
		{in: " Presence ", want: ModePresence, enforced: true},
		{in: "presence-strict", want: ModeStrict, enforced: true, strict: true},
		{in: "touchid", err: true},
	} {
		m, err := ParseMode(tc.in)
		if (err != nil) != tc.err {
			t.Fatalf("%q: err %v", tc.in, err)
		}
		if tc.err {
			continue
		}
		if m != tc.want || m.Enforced() != tc.enforced || m.Strict() != tc.strict {
			t.Fatalf("%q: %q enforced=%v strict=%v", tc.in, m, m.Enforced(), m.Strict())
		}
	}
}

func TestEnrolmentStatementBindsKeyMachineUserAndTime(t *testing.T) {
	priv, k := newKey(t)
	at := issued.Add(300 * time.Millisecond)
	msg := EnrolmentMessage(k.ID, "mac", "felix", at)
	if !strings.HasPrefix(string(msg), EnrolmentDomain) || !strings.Contains(string(msg), "at=20:2026-10-24T12:00:00Z\n") {
		t.Fatalf("message %q", msg)
	}
	sig := sign(t, priv, msg)
	if !VerifyEnrolment(k, "mac", "felix", at, sig) {
		t.Fatal("a valid enrolment statement did not verify")
	}
	for name, ok := range map[string]bool{
		"other machine": VerifyEnrolment(k, "other", "felix", at, sig),
		"other user":    VerifyEnrolment(k, "mac", "root", at, sig),
		"other time":    VerifyEnrolment(k, "mac", "felix", at.Add(time.Second), sig),
	} {
		if ok {
			t.Errorf("%s verified", name)
		}
	}
	// An approval signature is never an enrolment statement.
	_, other := newKey(t)
	if VerifyEnrolment(other, "mac", "felix", at, sig) {
		t.Fatal("another key verified the statement")
	}
}

func TestVerifyRotation(t *testing.T) {
	oldPriv, old := newKey(t)
	_, next := newKey(t)
	r := Rotation{From: old.ID, To: next.ID, At: issued}
	r.Sig = sign(t, oldPriv, RotationMessage(r.From, r.To, r.At))
	if !VerifyRotation(old, r) {
		t.Fatal("signed rotation did not verify")
	}
	r.To = old.ID
	if VerifyRotation(old, r) {
		t.Fatal("an altered rotation verified")
	}
}

// A routing upgrade approval is a Proof over the route's key: it verifies
// against RoutingExpect and against nothing else.
func TestRoutingApprovalVerifies(t *testing.T) {
	priv, k := newKey(t)
	raised := issued.Add(-time.Minute)
	key := "anthropic|claude-sonnet-4|claude-opus-4"
	trust := Trust{State: TrustPinned, Key: k, ValidFrom: issued.Add(-time.Hour)}
	e := RoutingExpect(key, raised, issued)
	p := signProof(t, priv, Proof{
		V: Version, Key: k.ID, Decision: key, Rule: RoutingRule, Scope: ScopeSession,
		Challenge: e.Challenge, Crossed: CrossingsDigest(nil), Issued: issued,
	})
	if res := Verify(p, e, trust); !res.OK() {
		t.Fatalf("routing approval refused: %s", res.Reason)
	}
	if res := Verify(p, RoutingExpect("anthropic|a|b", raised, issued), trust); res.Reason != ReasonDecision {
		t.Fatalf("another route: %s", res.Reason)
	}
	if res := Verify(p, RoutingExpect(key, raised.Add(time.Second), issued), trust); res.Reason != ReasonChallenge {
		t.Fatalf("another raising: %s", res.Reason)
	}
	if RoutingChallenge(key, raised) == RoutingChallenge(key+"x", raised) {
		t.Fatal("the challenge does not depend on the key")
	}
}
