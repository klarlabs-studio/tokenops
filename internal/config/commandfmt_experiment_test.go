package config

import "testing"

func TestCommandFmtExperimentBounds(t *testing.T) {
	for _, n := range []int{0, 1, 50} {
		cfg := Default()
		cfg.Optimizer.CommandFmt.Experiment = n
		if err := cfg.Validate(); err != nil {
			t.Errorf("experiment %d rejected: %v", n, err)
		}
	}
	for _, n := range []int{-1, 51} {
		cfg := Default()
		cfg.Optimizer.CommandFmt.Experiment = n
		if err := cfg.Validate(); err == nil {
			t.Errorf("experiment %d accepted", n)
		}
	}
}
