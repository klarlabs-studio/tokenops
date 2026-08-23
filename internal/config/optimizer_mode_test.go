package config

import "testing"

// The optimizer had two states hidden inside a daemon-wide mode: apply
// everything, or do nothing. There was no way to say "propose it and let
// me decide", even though the preferred-model ceiling already worked that
// way for upgrades.
func TestOptimizerModeValues(t *testing.T) {
	for _, tc := range []struct {
		raw             string
		wantApply       bool
		wantPropose     bool
		wantObserveOnly bool
	}{
		{"automatic", true, false, false},
		{"in_request", false, true, false},
		{"off", false, false, true},
		{"", false, false, true}, // unset stays observe-only
	} {
		m := OptimizerMode(tc.raw)
		if got := m.Applies(); got != tc.wantApply {
			t.Errorf("%q Applies() = %v, want %v", tc.raw, got, tc.wantApply)
		}
		if got := m.Proposes(); got != tc.wantPropose {
			t.Errorf("%q Proposes() = %v, want %v", tc.raw, got, tc.wantPropose)
		}
		if got := m.ObserveOnly(); got != tc.wantObserveOnly {
			t.Errorf("%q ObserveOnly() = %v, want %v", tc.raw, got, tc.wantObserveOnly)
		}
	}
}

// Defaulting to anything that changes a request would be wrong: an
// operator who upgrades should not silently start having their model
// swapped.
func TestOptimizerModeDefaultsToObserve(t *testing.T) {
	c := Default()
	if !c.Optimizer.Mode.ObserveOnly() {
		t.Errorf("default optimizer mode = %q, want observe-only", c.Optimizer.Mode)
	}
}

// A misspelling must fail loudly rather than silently disabling the
// optimizer — "automtic" should not read as "off".
func TestUnknownOptimizerModeRejected(t *testing.T) {
	c := Default()
	c.Optimizer.Mode = "automtic"
	err := c.Validate()
	if err == nil {
		t.Fatal("expected a validation error for a misspelled optimizer mode")
	}
	if !containsAll(err.Error(), "optimizer.mode") {
		t.Errorf("error should name the field: %v", err)
	}
}

// The three real values load.
func TestValidOptimizerModesAccepted(t *testing.T) {
	for _, m := range []OptimizerMode{"", OptimizerAutomatic, OptimizerInRequest, OptimizerOff} {
		c := Default()
		c.Optimizer.Mode = m
		if err := c.Validate(); err != nil {
			t.Errorf("optimizer mode %q rejected: %v", m, err)
		}
	}
}
