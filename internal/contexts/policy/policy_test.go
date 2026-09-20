package policy_test

import (
	"testing"

	"go.klarlabs.de/tokenops/internal/contexts/policy"
)

// The intent asks for one control ladder: observe-only → recommend →
// require approval → automatic. The code had four, none of which agreed
// with each other, so "how much authority has TokenOps been given" was a
// per-subsystem opinion an operator had to assemble by hand.
func TestTheLadderIsOrdered(t *testing.T) {
	rungs := []policy.Authority{
		policy.ObserveOnly, policy.Recommend, policy.RequireApproval, policy.Automatic,
	}
	for i := 1; i < len(rungs); i++ {
		if !rungs[i].AtLeast(rungs[i-1]) {
			t.Errorf("%q is not at least %q", rungs[i], rungs[i-1])
		}
		if rungs[i-1].AtLeast(rungs[i]) {
			t.Errorf("%q outranks %q", rungs[i-1], rungs[i])
		}
	}
}

// The two questions every subsystem actually asks. Keeping them as
// methods on the ladder is what stops each one re-deriving the answer
// from its own vocabulary and getting it subtly different.
func TestWhatEachRungPermits(t *testing.T) {
	cases := []struct {
		rung     policy.Authority
		maySpeak bool
		mayAct   bool
		needsNod bool
	}{
		{policy.ObserveOnly, false, false, false},
		{policy.Recommend, true, false, false},
		{policy.RequireApproval, true, true, true},
		{policy.Automatic, true, true, false},
	}
	for _, tc := range cases {
		t.Run(string(tc.rung), func(t *testing.T) {
			if got := tc.rung.MaySpeak(); got != tc.maySpeak {
				t.Errorf("MaySpeak = %v, want %v", got, tc.maySpeak)
			}
			if got := tc.rung.MayAct(); got != tc.mayAct {
				t.Errorf("MayAct = %v, want %v", got, tc.mayAct)
			}
			if got := tc.rung.NeedsApproval(); got != tc.needsNod {
				t.Errorf("NeedsApproval = %v, want %v", got, tc.needsNod)
			}
		})
	}
}

// Observe-only is the zero value, so a subsystem whose authority nobody
// configured does nothing rather than everything. A control system that
// fails open is worse than one that fails silent.
func TestTheZeroAuthorityObservesOnly(t *testing.T) {
	var a policy.Authority
	if a != policy.ObserveOnly {
		t.Errorf("zero authority = %q, want observe_only", a)
	}
	if a.MayAct() {
		t.Error("an unconfigured subsystem may act")
	}
}

// config.Mode: passive | active, daemon-wide.
func TestDaemonModeMapsOntoTheLadder(t *testing.T) {
	if got := policy.FromDaemonMode("passive"); got != policy.ObserveOnly {
		t.Errorf("passive = %q", got)
	}
	if got := policy.FromDaemonMode("active"); got != policy.Automatic {
		t.Errorf("active = %q", got)
	}
	// An empty mode is documented as passive.
	if got := policy.FromDaemonMode(""); got != policy.ObserveOnly {
		t.Errorf("empty = %q, want observe_only", got)
	}
	// Anything unrecognised is the safe rung, not the permissive one.
	if got := policy.FromDaemonMode("banana"); got != policy.ObserveOnly {
		t.Errorf("an unknown mode = %q; an unreadable setting must not grant authority", got)
	}
}

// CoachingConfig.Delivery: observe | advise | intervene. This is the
// ladder that already had three rungs, and the only one whose middle
// rung maps to recommend.
func TestCoachingDeliveryMapsOntoTheLadder(t *testing.T) {
	cases := map[string]policy.Authority{
		"observe":   policy.ObserveOnly,
		"advise":    policy.Recommend,
		"intervene": policy.Automatic,
		"":          policy.ObserveOnly,
		"nonsense":  policy.ObserveOnly,
	}
	for in, want := range cases {
		if got := policy.FromCoachingDelivery(in); got != want {
			t.Errorf("%q = %q, want %q", in, got, want)
		}
	}
}

// readguard.Mode: observe | active. Two rungs with the same words as
// the daemon's, meaning something different.
func TestReadGuardModeMapsOntoTheLadder(t *testing.T) {
	if got := policy.FromReadGuardMode("observe"); got != policy.ObserveOnly {
		t.Errorf("observe = %q", got)
	}
	if got := policy.FromReadGuardMode("active"); got != policy.Automatic {
		t.Errorf("active = %q", got)
	}
	if got := policy.FromReadGuardMode(""); got != policy.ObserveOnly {
		t.Errorf("empty = %q", got)
	}
}

// routingapproval is a propose/decide gate expressed as neither a mode
// nor a delivery. Its whole existence is the require-approval rung,
// which no other ladder can express.
func TestRoutingApprovalIsTheApprovalRung(t *testing.T) {
	if got := policy.FromRoutingApproval(true); got != policy.RequireApproval {
		t.Errorf("gated routing = %q, want require_approval", got)
	}
	if got := policy.FromRoutingApproval(false); got != policy.Automatic {
		t.Errorf("ungated routing = %q, want automatic", got)
	}
}

// The reason to unify: an operator asking "what can TokenOps do without
// me" gets one answer. The effective authority of a subsystem is the
// lesser of its own setting and the daemon's, so turning the daemon down
// turns everything down.
func TestEffectiveAuthorityIsTheLesserOfTheTwo(t *testing.T) {
	// A daemon in passive mode holds back a coach set to intervene.
	got := policy.Effective(policy.ObserveOnly, policy.Automatic)
	if got != policy.ObserveOnly {
		t.Errorf("effective = %q; a passive daemon did not hold back an "+
			"intervening subsystem", got)
	}
	// And the subsystem's own setting still limits it under an active
	// daemon.
	got = policy.Effective(policy.Automatic, policy.Recommend)
	if got != policy.Recommend {
		t.Errorf("effective = %q, want recommend", got)
	}
}

// Every rung must render to something an operator would recognise, and
// parse back. A ladder that cannot round-trip cannot be configured.
func TestEveryRungRoundTrips(t *testing.T) {
	for _, a := range policy.Ladder() {
		back, err := policy.Parse(string(a))
		if err != nil {
			t.Errorf("parse(%q): %v", a, err)
			continue
		}
		if back != a {
			t.Errorf("%q round-tripped to %q", a, back)
		}
		if a.Describe() == "" {
			t.Errorf("%q has no description", a)
		}
	}
}

// An unparseable rung is an error, not a silent default. Config
// validation should refuse it rather than quietly observing.
func TestParseRefusesAnUnknownRung(t *testing.T) {
	if _, err := policy.Parse("do-whatever"); err == nil {
		t.Error("an unknown rung parsed without error")
	}
}

// The ladder is what a surface enumerates when it offers the choice.
func TestLadderIsInAscendingOrder(t *testing.T) {
	l := policy.Ladder()
	if len(l) != 4 {
		t.Fatalf("want 4 rungs, got %v", l)
	}
	for i := 1; i < len(l); i++ {
		if !l[i].AtLeast(l[i-1]) {
			t.Errorf("Ladder() is not ascending: %v", l)
		}
	}
}

// A fifth ladder, with a fourth vocabulary, that the audit missed:
// smart_routing.intervention is off | advise | delegate | auto.
//
// "delegate" is its own rung and maps to require-approval rather than
// automatic: it marks work that *may* be handed to a subagent on a
// cheaper model, which is a proposal awaiting a decision, not an action
// taken. Mapping it to automatic would report more authority than the
// operator granted, which is the direction that matters.
func TestSmartRoutingInterventionMapsOntoTheLadder(t *testing.T) {
	cases := map[string]policy.Authority{
		"off":      policy.ObserveOnly,
		"advise":   policy.Recommend,
		"delegate": policy.RequireApproval,
		"auto":     policy.Automatic,
		// Empty is documented as advise — the one mapping whose default
		// is not the bottom rung, because the config says so.
		"":         policy.Recommend,
		"nonsense": policy.ObserveOnly,
	}
	for in, want := range cases {
		if got := policy.FromSmartRoutingIntervention(in); got != want {
			t.Errorf("%q = %q, want %q", in, got, want)
		}
	}
}
