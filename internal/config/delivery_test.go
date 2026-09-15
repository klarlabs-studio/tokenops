package config

import "testing"

func TestParseDeliveryDefaultsToAdvise(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"observe", DeliveryObserve},
		{"OBSERVE", DeliveryObserve},
		{"  intervene  ", DeliveryIntervene},
		{"advise", DeliveryAdvise},
		{"", DeliveryAdvise},
		// An unrecognised level resolves to the middle rung, never the top
		// one. Absorbing a mistake into "intervene" would let it start
		// blocking the agent's reads on the strength of a typo.
		{"bossy", DeliveryAdvise},
	} {
		if got := ParseDelivery(tc.in); got != tc.want {
			t.Errorf("ParseDelivery(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestValidateDeliveryRejectsTypos(t *testing.T) {
	for _, ok := range []string{"", "observe", "advise", "intervene", "INTERVENE"} {
		if err := ValidateDelivery(ok); err != nil {
			t.Errorf("ValidateDelivery(%q) = %v, want nil", ok, err)
		}
	}
	if err := ValidateDelivery("bossy"); err == nil {
		t.Error("ValidateDelivery accepted an unknown level; the config surface must reject what ParseDelivery absorbs")
	}
}

func TestRungsAreCumulativeAndGradedByInterference(t *testing.T) {
	for _, tc := range []struct {
		delivery             string
		advice, intervention bool
	}{
		// Empty is advise: exactly what the hooks did before this key
		// existed, so upgrading changes nothing until the operator says so.
		{"", true, false},
		{DeliveryObserve, false, false},
		{DeliveryAdvise, true, false},
		{DeliveryIntervene, true, true},
	} {
		c := CoachingConfig{Delivery: tc.delivery}
		if got := c.AllowsAdvice(); got != tc.advice {
			t.Errorf("delivery %q: AllowsAdvice() = %v, want %v", tc.delivery, got, tc.advice)
		}
		if got := c.AllowsIntervention(); got != tc.intervention {
			t.Errorf("delivery %q: AllowsIntervention() = %v, want %v", tc.delivery, got, tc.intervention)
		}
		// Asking is never an interruption: every rung answers a direct
		// question, including observe.
		if !c.AllowsPull() {
			t.Errorf("delivery %q: AllowsPull() = false, want true at every level", tc.delivery)
		}
		// The ladder only ever climbs: nothing intervenes without also
		// advising.
		if c.AllowsIntervention() && !c.AllowsAdvice() {
			t.Errorf("delivery %q intervenes without advising — the rungs are not cumulative", tc.delivery)
		}
	}
}

func TestValidateRejectsUnknownDelivery(t *testing.T) {
	cfg := Default()
	cfg.Coaching.Delivery = "bossy"
	if err := cfg.Validate(); err == nil {
		t.Fatal("Validate accepted an unknown delivery level")
	}
	cfg.Coaching.Delivery = DeliveryIntervene
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate rejected a valid delivery: %v", err)
	}
}
