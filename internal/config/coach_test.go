package config

import "testing"

func TestCoachPowerPrecedence(t *testing.T) {
	c := Default()
	c.Coaching.Delivery = DeliveryIntervene
	c.Optimizer.SmartRouting.Enabled = true
	c.Optimizer.SmartRouting.Intervention = "delegate"

	// Old keys map in when no coach block exists.
	for power, want := range map[string]PowerSetting{
		PowerInform: {AutonomyAdvise, "coaching.delivery"},
		PowerWaste:  {AutonomyAutonomous, "coaching.delivery"},
		PowerModels: {AutonomyAsk, "optimizer.smart_routing.intervention"},
	} {
		if got := c.CoachPower(power); got != want {
			t.Errorf("legacy %s = %+v, want %+v", power, got, want)
		}
	}

	// coach.autonomy wins over every old key.
	c.Coach.Autonomy = AutonomyAdvise
	for _, p := range Powers() {
		if got := c.CoachPower(p); got.Rung != AutonomyAdvise || got.Source != "coach.autonomy" {
			t.Errorf("coach.autonomy %s = %+v", p, got)
		}
	}

	// A per-power setting wins over coach.autonomy.
	c.Coach.Powers = map[string]string{PowerWaste: AutonomyAutonomous}
	if got := c.CoachPower(PowerWaste); got != (PowerSetting{AutonomyAutonomous, "coach.powers.waste"}) {
		t.Errorf("per-power waste = %+v", got)
	}
}

func TestCoachLegacyMapping(t *testing.T) {
	cases := []struct {
		delivery, intervention string
		enabled                bool
		inform, waste, models  string
	}{
		{"observe", "advise", true, AutonomyOff, AutonomyOff, AutonomyAdvise},
		{"advise", "", true, AutonomyAdvise, AutonomyAdvise, AutonomyAdvise},
		{"", "auto", true, AutonomyAdvise, AutonomyAdvise, AutonomyAutonomous},
		{"intervene", "off", true, AutonomyAdvise, AutonomyAutonomous, AutonomyOff},
		{"advise", "auto", false, AutonomyAdvise, AutonomyAdvise, AutonomyOff},
	}
	for _, tc := range cases {
		c := Default()
		c.Coaching.Delivery = tc.delivery
		c.Optimizer.SmartRouting.Enabled = tc.enabled
		c.Optimizer.SmartRouting.Intervention = tc.intervention
		got := [3]string{c.CoachPower(PowerInform).Rung, c.CoachPower(PowerWaste).Rung, c.CoachPower(PowerModels).Rung}
		want := [3]string{tc.inform, tc.waste, tc.models}
		if got != want {
			t.Errorf("delivery=%q intervention=%q enabled=%v: got %v want %v", tc.delivery, tc.intervention, tc.enabled, got, want)
		}
	}
}

func TestCoachVerbosity(t *testing.T) {
	c := Default()
	if v, src := c.CoachVerbosity(); v != VerbosityNormal || src != "default" {
		t.Errorf("default verbosity = %q from %q", v, src)
	}
	c.Coach.Verbosity = VerbosityQuiet
	if v, src := c.CoachVerbosity(); v != VerbosityQuiet || src != "coach.verbosity" {
		t.Errorf("verbosity = %q from %q", v, src)
	}
}

func TestCoachValidation(t *testing.T) {
	for name, cc := range map[string]CoachConfig{
		"bad autonomy":  {Autonomy: "yolo"},
		"bad verbosity": {Verbosity: "chatty"},
		"bad power":     {Powers: map[string]string{"billing": AutonomyAdvise}},
		"bad rung":      {Powers: map[string]string{PowerModels: "maybe"}},
	} {
		c := Default()
		c.Coach = cc
		if err := c.Validate(); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	c := Default()
	c.Coach = CoachConfig{Autonomy: " Ask ", Verbosity: "VERBOSE", Powers: map[string]string{PowerWaste: "autonomous"}}
	if err := c.Validate(); err != nil {
		t.Errorf("valid coach block rejected: %v", err)
	}
	if got := c.CoachPower(PowerInform).Rung; got != AutonomyAsk {
		t.Errorf("autonomy not normalised: %q", got)
	}
}

// The holdout is on at 10% unless the operator opts out, and never more.
func TestCoachHoldoutPercent(t *testing.T) {
	pct := func(n int) *int { return &n }
	for _, tc := range []struct {
		set  *int
		want int
	}{{nil, DefaultHoldoutPercent}, {pct(0), 0}, {pct(5), 5}, {pct(10), 10}} {
		c := Default()
		c.Coach.Holdout = tc.set
		if got := c.CoachHoldoutPercent(); got != tc.want {
			t.Errorf("holdout %v = %d, want %d", tc.set, got, tc.want)
		}
		if err := c.Validate(); err != nil {
			t.Errorf("holdout %v rejected: %v", tc.set, err)
		}
	}
	for _, bad := range []int{-1, 11, 50} {
		c := Default()
		c.Coach.Holdout = pct(bad)
		if err := c.Validate(); err == nil {
			t.Errorf("holdout %d accepted", bad)
		}
	}
}

func TestInterruptionsPerDay(t *testing.T) {
	for _, tc := range []struct{ set, want int }{{0, DefaultInterruptionsPerDay}, {-1, 0}, {3, 3}} {
		if got := (QuietConfig{MaxPerDay: tc.set}).InterruptionsPerDay(); got != tc.want {
			t.Errorf("max_per_day %d = %d, want %d", tc.set, got, tc.want)
		}
	}
}
