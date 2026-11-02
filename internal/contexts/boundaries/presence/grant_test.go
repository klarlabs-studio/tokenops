package presence

import (
	"testing"
	"time"
)

func baseGrant(k Key) Grant {
	return Grant{
		V: Version, Key: k.ID, ID: "bnd-0123456789ab", Rules: []string{"headroom-pause"},
		Granter: "felix", Why: "release", At: issued, Until: issued.Add(30 * time.Minute),
	}
}

func grantExpect(g Grant) GrantExpect {
	return GrantExpect{ID: g.ID, Rules: g.Rules, Granter: g.Granter, Why: g.Why, At: g.At, Until: g.Until, Now: issued.Add(time.Minute)}
}

// A signed grant lifts only the grant it was made over, under the
// enrolled key, until its end.
func TestVerifyGrant(t *testing.T) {
	priv, k := newKey(t)
	otherPriv, other := newKey(t)
	signed := func(g Grant) *Grant {
		msg, err := g.Message()
		if err != nil {
			t.Fatal(err)
		}
		g.Sig = sign(t, priv, msg)
		return &g
	}
	cases := []struct {
		name  string
		grant func() *Grant
		exp   func(*GrantExpect)
		trust func() Trust
		want  Reason
	}{
		{name: "valid", want: ReasonNone},
		{name: "unsigned", grant: func() *Grant { return nil }, want: ReasonUnsigned},
		{name: "not enrolled", trust: func() Trust { return Trust{State: TrustNotEnrolled} }, want: ReasonNotEnrolled},
		{name: "signature missing", grant: func() *Grant { g := baseGrant(k); return &g }, want: ReasonSignature},
		{name: "until stretched in the file", exp: func(e *GrantExpect) { e.Until = e.Until.Add(time.Hour) }, want: ReasonGrantAltered},
		{name: "start moved in the file", exp: func(e *GrantExpect) { e.At = e.At.Add(-time.Hour) }, want: ReasonGrantAltered},
		{name: "granter changed in the file", exp: func(e *GrantExpect) { e.Granter = "root" }, want: ReasonGrantAltered},
		{name: "rule added in the file", exp: func(e *GrantExpect) { e.Rules = []string{"headroom-pause", "session-spend"} }, want: ReasonRule},
		{name: "rule swapped in the file", exp: func(e *GrantExpect) { e.Rules = []string{"session-spend"} }, want: ReasonRule},
		{name: "another grant's id", exp: func(e *GrantExpect) { e.ID = "bnd-ffffffffffff" }, want: ReasonDecision},
		{name: "until edited in the statement and the file", grant: func() *Grant {
			g := *signed(baseGrant(k))
			g.Until = g.Until.Add(time.Hour)
			return &g
		}, exp: func(e *GrantExpect) { e.Until = e.Until.Add(time.Hour) }, want: ReasonSignature},
		{name: "rule edited in the statement and the file", grant: func() *Grant {
			g := *signed(baseGrant(k))
			g.Rules = []string{"session-spend"}
			return &g
		}, exp: func(e *GrantExpect) { e.Rules = []string{"session-spend"} }, want: ReasonSignature},
		{name: "wrong key, naming itself", grant: func() *Grant {
			g := baseGrant(other)
			msg, _ := g.Message()
			g.Sig = sign(t, otherPriv, msg)
			return &g
		}, want: ReasonWrongKey},
		{name: "wrong key, naming the enrolled one", grant: func() *Grant {
			g := baseGrant(k)
			msg, _ := g.Message()
			g.Sig = sign(t, otherPriv, msg)
			return &g
		}, want: ReasonSignature},
		{name: "key held", trust: func() Trust { tr := pinned(k); tr.State = TrustHeld; return tr }, want: ReasonKeyHeld},
		{name: "before the key was trusted", trust: func() Trust {
			tr := pinned(k)
			tr.ValidFrom = issued.Add(time.Second)
			return tr
		}, want: ReasonBeforeKey},
		{name: "issued in the future", exp: func(e *GrantExpect) { e.Now = issued.Add(-2 * time.Minute) }, want: ReasonFuture},
		{name: "past its end", exp: func(e *GrantExpect) { e.Now = issued.Add(30 * time.Minute) }, want: ReasonExpired},
		{name: "over a day", grant: func() *Grant {
			g := baseGrant(k)
			g.Until = g.At.Add(MaxGrant + time.Second)
			return &g
		}, exp: func(e *GrantExpect) { e.Until = e.At.Add(MaxGrant + time.Second) }, want: ReasonMalformed},
		{name: "no rules", grant: func() *Grant { g := baseGrant(k); g.Rules = nil; return &g }, want: ReasonMalformed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := signed(baseGrant(k))
			if tc.grant != nil {
				g = tc.grant()
			}
			e := grantExpect(baseGrant(k))
			if tc.exp != nil {
				tc.exp(&e)
			}
			tr := pinned(k)
			if tc.trust != nil {
				tr = tc.trust()
			}
			if got := VerifyGrant(g, e, tr); got.Reason != tc.want {
				t.Fatalf("VerifyGrant = %q (%s), want %q", got.Reason, got.Reason.Phrase(), tc.want)
			}
		})
	}
}

// A grant signature is not an approval, and an approval signature is not
// a grant: the domains differ.
func TestGrantAndApprovalDomainsAreSeparate(t *testing.T) {
	priv, k := newKey(t)
	g := baseGrant(k)
	gm, err := g.Message()
	if err != nil {
		t.Fatal(err)
	}
	pm, err := baseProof(k).Message()
	if err != nil {
		t.Fatal(err)
	}
	if string(gm[:len(GrantDomain)]) != GrantDomain || string(pm[:len(GrantDomain)]) == GrantDomain {
		t.Fatalf("domains: %q %q", gm, pm)
	}
	p := baseProof(k)
	p.Sig = sign(t, priv, gm)
	if Verify(&p, expect(), pinned(k)).OK() {
		t.Fatal("a grant signature verified as an approval")
	}
}

// The encoding is the contract the helper signs: pin it byte for byte.
func TestGrantMessageEncoding(t *testing.T) {
	g := Grant{
		V: 1, Key: "pk-0123456789abcdef", ID: "bnd-0123456789ab", Rules: []string{"headroom-pause", "a\nrule=1:x"},
		Granter: "felix", Why: "", At: issued, Until: issued.Add(30 * time.Minute),
	}
	got, err := g.Message()
	if err != nil {
		t.Fatal(err)
	}
	want := "tokenops.extra-allowance/v1\n" +
		"key=19:pk-0123456789abcdef\n" +
		"id=16:bnd-0123456789ab\n" +
		"rules=1:2\n" +
		"rule=14:headroom-pause\n" +
		"rule=10:a\nrule=1:x\n" +
		"granter=5:felix\n" +
		"why=0:\n" +
		"at=20:2026-10-24T12:00:00Z\n" +
		"until=20:2026-10-24T12:30:00Z\n"
	if string(got) != want {
		t.Fatalf("message\n%q\nwant\n%q", got, want)
	}
}
