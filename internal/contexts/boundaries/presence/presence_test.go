package presence

import (
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"strings"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/boundaries"
)

// Software P-256 keys stand in for the Secure Enclave: the verifier
// cannot tell them apart (ADR 0015 §7), and the tests raise no dialog.
func newKey(t testing.TB) (*ecdsa.PrivateKey, Key) {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := priv.PublicKey.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	k, err := ParseKey(raw)
	if err != nil {
		t.Fatal(err)
	}
	return priv, k
}

func sign(t testing.TB, priv *ecdsa.PrivateKey, msg []byte) []byte {
	t.Helper()
	sum := sha256.Sum256(msg)
	sig, err := ecdsa.SignASN1(rand.Reader, priv, sum[:])
	if err != nil {
		t.Fatal(err)
	}
	return sig
}

func signProof(t testing.TB, priv *ecdsa.PrivateKey, p Proof) *Proof {
	t.Helper()
	msg, err := p.Message()
	if err != nil {
		t.Fatal(err)
	}
	p.Sig = sign(t, priv, msg)
	return &p
}

var (
	issued    = time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	crossings = []boundaries.Crossing{{Field: boundaries.FieldSessionCost, Op: ">=", Value: 23.1, Threshold: 20}}
)

func baseProof(k Key) Proof {
	return Proof{
		V: Version, Key: k.ID, Decision: "bnd-0123456789ab", Rule: "session-spend", Session: "s1",
		Scope: ScopeOnce, Challenge: "c-1", Crossed: CrossingsDigest(crossings), Issued: issued,
	}
}

func expect() Expect {
	return Expect{
		Decision: "bnd-0123456789ab", Rule: "session-spend", Session: "s1", Scope: ScopeOnce,
		Challenge: "c-1", Crossings: crossings, Now: issued.Add(time.Minute),
	}
}

func pinned(k Key) Trust {
	return Trust{State: TrustPinned, Key: k, ValidFrom: issued.Add(-time.Hour)}
}

// The cases of ADR 0015's validation step 2, and the ones that pass.
func TestVerify(t *testing.T) {
	priv, k := newKey(t)
	otherPriv, other := newKey(t)
	spentProof := signProof(t, priv, baseProof(k))

	cases := []struct {
		name  string
		proof func() *Proof
		exp   func(*Expect)
		trust func() Trust
		want  Reason
	}{
		{name: "valid one-step", want: ReasonNone},
		{name: "valid session", proof: func() *Proof {
			p := baseProof(k)
			p.Scope = ScopeSession
			return signProof(t, priv, p)
		}, exp: func(e *Expect) { e.Scope, e.Now = ScopeSession, issued.Add(23*time.Hour) }, want: ReasonNone},
		{name: "not enrolled", trust: func() Trust { return Trust{State: TrustNotEnrolled} }, want: ReasonNotEnrolled},
		{name: "unsigned", proof: func() *Proof { return nil }, want: ReasonUnsigned},
		{name: "signature missing", proof: func() *Proof { p := baseProof(k); return &p }, want: ReasonSignature},
		{name: "wrong key, naming itself", proof: func() *Proof {
			p := baseProof(other)
			return signProof(t, otherPriv, p)
		}, want: ReasonWrongKey},
		{name: "wrong key, naming the enrolled one", proof: func() *Proof {
			return signProof(t, otherPriv, baseProof(k))
		}, want: ReasonSignature},
		{name: "wrong decision", exp: func(e *Expect) { e.Decision = "bnd-ffffffffffff" }, want: ReasonDecision},
		{name: "wrong rule", exp: func(e *Expect) { e.Rule = "plan-floor" }, want: ReasonRule},
		{name: "wrong session", exp: func(e *Expect) { e.Session = "s2" }, want: ReasonSession},
		{name: "one-step stretched to session", exp: func(e *Expect) { e.Scope = ScopeSession }, want: ReasonScope},
		{name: "wrong challenge", exp: func(e *Expect) { e.Challenge = "c-2" }, want: ReasonChallenge},
		{name: "no challenge on the decision", exp: func(e *Expect) { e.Challenge = "" }, want: ReasonChallenge},
		{name: "altered crossings", exp: func(e *Expect) {
			e.Crossings = []boundaries.Crossing{{Field: boundaries.FieldSessionCost, Op: ">=", Value: 230.1, Threshold: 20}}
		}, want: ReasonCrossings},
		{name: "field edited after signing", proof: func() *Proof {
			p := *signProof(t, priv, baseProof(k))
			p.Issued = issued.Add(5 * time.Minute)
			return &p
		}, want: ReasonSignature},
		{name: "one-step expired", exp: func(e *Expect) { e.Now = issued.Add(OnceTTL + time.Second) }, want: ReasonExpired},
		{name: "session expired", proof: func() *Proof {
			p := baseProof(k)
			p.Scope = ScopeSession
			return signProof(t, priv, p)
		}, exp: func(e *Expect) { e.Scope, e.Now = ScopeSession, issued.Add(SessionTTL+time.Second) }, want: ReasonExpired},
		{name: "issued in the future", exp: func(e *Expect) { e.Now = issued.Add(-2 * time.Minute) }, want: ReasonFuture},
		{name: "one-step reused after consumption", proof: func() *Proof { return spentProof },
			exp: func(e *Expect) {
				used := spentProof.Fingerprint()
				e.Spent = func(fp string) bool { return fp == used }
			}, want: ReasonSpent},
		{name: "issued before the key was trusted", trust: func() Trust {
			tr := pinned(k)
			tr.ValidFrom = issued.Add(time.Second)
			return tr
		}, want: ReasonBeforeKey},
		{name: "key held after an unsigned replacement", trust: func() Trust {
			tr := pinned(k)
			tr.State = TrustHeld
			return tr
		}, want: ReasonKeyHeld},
		{name: "malformed version", proof: func() *Proof {
			p := baseProof(k)
			p.V = 2
			return &p
		}, want: ReasonMalformed},
		{name: "malformed sub-second issue time", proof: func() *Proof {
			p := baseProof(k)
			p.Issued = issued.Add(time.Millisecond)
			return &p
		}, want: ReasonMalformed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := signProof(t, priv, baseProof(k))
			if tc.proof != nil {
				p = tc.proof()
			}
			e := expect()
			if tc.exp != nil {
				tc.exp(&e)
			}
			tr := pinned(k)
			if tc.trust != nil {
				tr = tc.trust()
			}
			got := Verify(p, e, tr)
			if got.Reason != tc.want {
				t.Fatalf("Verify = %q (%s), want %q", got.Reason, got.Reason.Phrase(), tc.want)
			}
			if got.OK() != (tc.want == ReasonNone) {
				t.Fatalf("OK() = %v for reason %q", got.OK(), got.Reason)
			}
		})
	}
}

// The encoding is the contract a signer in another language reproduces:
// pin it byte for byte.
func TestMessageEncoding(t *testing.T) {
	p := Proof{
		V: 1, Key: "pk-0123456789abcdef", Decision: "bnd-0123456789ab", Rule: "session-spend",
		Session: "a\nkey=1:x", Scope: "once", Challenge: "ch", Crossed: "cd", Issued: issued,
	}
	got, err := p.Message()
	if err != nil {
		t.Fatal(err)
	}
	want := "tokenops.approval/v1\n" +
		"key=19:pk-0123456789abcdef\n" +
		"decision=16:bnd-0123456789ab\n" +
		"rule=13:session-spend\n" +
		"session=9:a\nkey=1:x\n" +
		"scope=4:once\n" +
		"challenge=2:ch\n" +
		"crossed=2:cd\n" +
		"issued=20:2026-10-24T12:00:00Z\n"
	if string(got) != want {
		t.Fatalf("message:\n%q\nwant\n%q", got, want)
	}
	// A value holding what looks like the next field cannot be read as it:
	// moving bytes between fields changes the message.
	q := p
	q.Session, q.Scope = "a", "once"
	if m, _ := q.Message(); string(m) == string(got) {
		t.Fatal("different statements encode alike")
	}
}

func TestMessageRefusesWhatItCannotEncodeCanonically(t *testing.T) {
	for name, edit := range map[string]func(*Proof){
		"scope":        func(p *Proof) { p.Scope = "forever" },
		"no challenge": func(p *Proof) { p.Challenge = "" },
		"zone":         func(p *Proof) { p.Issued = issued.In(time.FixedZone("CEST", 2*3600)) },
		"zero time":    func(p *Proof) { p.Issued = time.Time{} },
	} {
		p := baseProof(Key{ID: "pk-x"})
		edit(&p)
		if _, err := p.Message(); err == nil {
			t.Errorf("%s: Message accepted %+v", name, p)
		}
	}
}

// wantDigest was computed outside Go (a few lines of Python following the
// encoding in CrossingsDigest's comment), so the pin checks the encoding
// is reproducible, not only that it is stable.
const wantDigest = "afca1c0233ac15fe4789fbece65ee61f50dab45dc10f9aa8db8d53c32d60c775"

func TestCrossingsDigestIsStableAndOrderSensitive(t *testing.T) {
	a := boundaries.Crossing{Field: boundaries.FieldSessionCost, Op: ">=", Value: 23.1, Threshold: 20}
	b := boundaries.Crossing{Field: boundaries.FieldPlanLeft, Op: "<", Value: 4, Threshold: 5, Window: boundaries.WindowWeek}
	// Pinned: a signer elsewhere must produce exactly this.
	if got := CrossingsDigest([]boundaries.Crossing{a, b}); got != wantDigest {
		t.Fatalf("digest = %s, want %s", got, wantDigest)
	}
	if CrossingsDigest([]boundaries.Crossing{a, b}) == CrossingsDigest([]boundaries.Crossing{b, a}) {
		t.Fatal("digest ignores order")
	}
	if CrossingsDigest(nil) == CrossingsDigest([]boundaries.Crossing{a}) {
		t.Fatal("no crossings digest like one")
	}
}

// The spike's route (crypto/ecdh, then a PKIX round trip) and ParseKey
// accept the same raw X9.63 point and yield the same key.
func TestParseKeyMatchesTheSpikesRoute(t *testing.T) {
	_, k := newKey(t)
	ek, err := ecdh.P256().NewPublicKey(k.Raw)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKIXPublicKey(ek)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := x509.ParsePKIXPublicKey(der)
	if err != nil {
		t.Fatal(err)
	}
	if !parsed.(*ecdsa.PublicKey).Equal(k.Public) {
		t.Fatal("ParseKey and the spike's route disagree")
	}
	if !strings.HasPrefix(k.ID, "pk-") || len(k.ID) != 19 {
		t.Fatalf("key id %q", k.ID)
	}
	bad := append([]byte(nil), k.Raw...)
	bad[64] ^= 1
	if _, err := ParseKey(bad); err == nil {
		t.Fatal("a point off the curve parsed")
	}
	if _, err := ParseKey(k.Raw[:33]); err == nil {
		t.Fatal("a compressed-length key parsed")
	}
}

func TestReconcile(t *testing.T) {
	oldPriv, oldKey := newKey(t)
	newPriv, nextKey := newKey(t)
	now := issued
	enrolled := Pin{KeyID: oldKey.ID, PublicKey: oldKey.Raw, Since: now.Add(-48 * time.Hour), How: PinEnrolled}

	t.Run("no key file is not enrolled and keeps the pin", func(t *testing.T) {
		tr, pin, changed := Reconcile(nil, nil, &enrolled, now)
		if tr.State != TrustNotEnrolled || changed || pin.KeyID != oldKey.ID {
			t.Fatalf("%+v %+v %v", tr, pin, changed)
		}
	})
	t.Run("first key is enrolled without a hold", func(t *testing.T) {
		tr, pin, changed := Reconcile(&oldKey, nil, nil, now)
		if tr.State != TrustPinned || !changed || pin.How != PinEnrolled || !pin.HoldUntil.IsZero() || !tr.ValidFrom.Equal(now) {
			t.Fatalf("%+v %+v %v", tr, pin, changed)
		}
	})
	t.Run("the pinned key stays pinned", func(t *testing.T) {
		tr, _, changed := Reconcile(&oldKey, nil, &enrolled, now)
		if tr.State != TrustPinned || changed || tr.Key.ID != oldKey.ID {
			t.Fatalf("%+v %v", tr, changed)
		}
	})
	t.Run("unsigned replacement is held for 15 minutes", func(t *testing.T) {
		tr, pin, changed := Reconcile(&nextKey, nil, &enrolled, now)
		if tr.State != TrustHeld || !changed || pin.How != PinReplaced || pin.Previous != oldKey.ID ||
			!pin.HoldUntil.Equal(now.Add(ReplacementHold)) {
			t.Fatalf("%+v %+v %v", tr, pin, changed)
		}
		// Seen again during the hold: still held, the hold not restarted.
		tr2, pin2, changed2 := Reconcile(&nextKey, nil, &pin, now.Add(10*time.Minute))
		if tr2.State != TrustHeld || changed2 || !pin2.HoldUntil.Equal(pin.HoldUntil) {
			t.Fatalf("%+v %+v %v", tr2, pin2, changed2)
		}
		after, _, _ := Reconcile(&nextKey, nil, &pin, now.Add(ReplacementHold))
		if after.State != TrustPinned || !after.ValidFrom.Equal(pin.HoldUntil) {
			t.Fatalf("after the hold: %+v", after)
		}
	})
	t.Run("rotation signed by the old key has no hold", func(t *testing.T) {
		at := now.Add(-time.Minute)
		rot := &Rotation{From: oldKey.ID, To: nextKey.ID, At: at, Sig: sign(t, oldPriv, RotationMessage(oldKey.ID, nextKey.ID, at))}
		tr, pin, changed := Reconcile(&nextKey, rot, &enrolled, now)
		if tr.State != TrustPinned || !changed || pin.How != PinRotated || !pin.HoldUntil.IsZero() || !tr.ValidFrom.Equal(at) {
			t.Fatalf("%+v %+v %v", tr, pin, changed)
		}
	})
	t.Run("rotation signed by the new key is a replacement", func(t *testing.T) {
		rot := &Rotation{From: oldKey.ID, To: nextKey.ID, At: now, Sig: sign(t, newPriv, RotationMessage(oldKey.ID, nextKey.ID, now))}
		tr, pin, _ := Reconcile(&nextKey, rot, &enrolled, now)
		if tr.State != TrustHeld || pin.How != PinReplaced {
			t.Fatalf("%+v %+v", tr, pin)
		}
	})
	t.Run("rotation to another key is a replacement", func(t *testing.T) {
		_, third := newKey(t)
		rot := &Rotation{From: oldKey.ID, To: third.ID, At: now, Sig: sign(t, oldPriv, RotationMessage(oldKey.ID, third.ID, now))}
		tr, _, _ := Reconcile(&nextKey, rot, &enrolled, now)
		if tr.State != TrustHeld {
			t.Fatalf("%+v", tr)
		}
	})
}

// End to end through Reconcile and Verify: what an approval is worth
// across an unsigned replacement and a signed rotation.
func TestApprovalsAcrossKeyChanges(t *testing.T) {
	oldPriv, oldKey := newKey(t)
	newPriv, nextKey := newKey(t)
	replacedAt := issued
	enrolled := Pin{KeyID: oldKey.ID, PublicKey: oldKey.Raw, Since: replacedAt.Add(-48 * time.Hour), How: PinEnrolled}
	_, pin, _ := Reconcile(&nextKey, nil, &enrolled, replacedAt)

	session := func(k Key, at time.Time) Proof {
		p := baseProof(k)
		p.Scope, p.Issued = ScopeSession, at
		return p
	}
	exp := func(now time.Time) Expect {
		e := expect()
		e.Scope, e.Now = ScopeSession, now
		return e
	}
	afterHold := replacedAt.Add(ReplacementHold + time.Minute)
	trAfter, _, _ := Reconcile(&nextKey, nil, &pin, afterHold)

	at := replacedAt.Add(-time.Minute)
	rot := &Rotation{From: oldKey.ID, To: nextKey.ID, At: at, Sig: sign(t, oldPriv, RotationMessage(oldKey.ID, nextKey.ID, at))}
	trRot, _, _ := Reconcile(&nextKey, rot, &enrolled, replacedAt)
	trHeld, _, _ := Reconcile(&nextKey, nil, &pin, replacedAt.Add(2*time.Minute))

	cases := []struct {
		name string
		p    *Proof
		e    Expect
		tr   Trust
		want Reason
	}{
		{"old key's proof from before the replacement", signProof(t, oldPriv, session(oldKey, replacedAt.Add(-time.Hour))),
			exp(afterHold), trAfter, ReasonWrongKey},
		{"new key's proof back-dated before the replacement", signProof(t, newPriv, session(nextKey, replacedAt.Add(-time.Hour))),
			exp(afterHold), trAfter, ReasonBeforeKey},
		{"new key's proof signed during the hold", signProof(t, newPriv, session(nextKey, replacedAt.Add(5*time.Minute))),
			exp(afterHold), trAfter, ReasonBeforeKey},
		{"any proof while held", signProof(t, newPriv, session(nextKey, replacedAt.Add(time.Minute))),
			exp(replacedAt.Add(2 * time.Minute)), trHeld, ReasonKeyHeld},
		{"new key's proof after the hold", signProof(t, newPriv, session(nextKey, afterHold)),
			exp(afterHold.Add(time.Minute)), trAfter, ReasonNone},
		{"valid, signed after a signed rotation", signProof(t, newPriv, session(nextKey, replacedAt)),
			exp(replacedAt.Add(time.Minute)), trRot, ReasonNone},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Verify(tc.p, tc.e, tc.tr); got.Reason != tc.want {
				t.Fatalf("Verify = %q, want %q", got.Reason, tc.want)
			}
		})
	}
}

func TestFingerprintIgnoresSignatureMalleation(t *testing.T) {
	priv, k := newKey(t)
	a := signProof(t, priv, baseProof(k))
	b := signProof(t, priv, baseProof(k))
	if string(a.Sig) == string(b.Sig) {
		t.Skip("signatures happened to match")
	}
	if a.Fingerprint() != b.Fingerprint() || a.Fingerprint() == "" {
		t.Fatal("two signatures over one statement fingerprint differently")
	}
}

func BenchmarkVerify(b *testing.B) {
	priv, k := newKey(b)
	p := signProof(b, priv, baseProof(k))
	e, tr := expect(), pinned(k)
	for b.Loop() {
		if !Verify(p, e, tr).OK() {
			b.Fatal("refused")
		}
	}
}
