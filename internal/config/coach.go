package config

import (
	"fmt"
	"sort"
	"strings"
)

// CoachConfig is the coach's two dials (ADR 0006): who decides when it sees
// something worth changing, and how much it says.
type CoachConfig struct {
	// Autonomy is the default rung for every power: off | advise | ask |
	// autonomous. Empty falls back to the older coaching and smart-routing
	// keys, so an existing config means what it meant.
	Autonomy string `yaml:"autonomy,omitempty"`
	// Verbosity is how much the coach says: quiet | normal | verbose.
	// Approval requests are always shown whatever it says here.
	Verbosity string `yaml:"verbosity,omitempty"`
	// Powers overrides Autonomy for one power: inform, waste, models, or
	// context.
	Powers map[string]string `yaml:"powers,omitempty"`
}

// Autonomy rungs, least to most autonomous. Each is a rung of the ADR 0004
// ladder: observe only, recommend, require approval, automatic.
const (
	AutonomyOff        = "off"
	AutonomyAdvise     = "advise"
	AutonomyAsk        = "ask"
	AutonomyAutonomous = "autonomous"
)

// Verbosity levels.
const (
	VerbosityQuiet   = "quiet"
	VerbosityNormal  = "normal"
	VerbosityVerbose = "verbose"
)

// The coach's powers.
const (
	PowerInform = "inform"
	PowerWaste  = "waste"
	PowerModels = "models"
	// PowerContext is when the agent compacts its context: advised by the
	// compact tip, or set in the agent's own settings when autonomous.
	PowerContext = "context"
)

// Powers lists the coach's powers in reporting order.
func Powers() []string { return []string{PowerInform, PowerWaste, PowerModels, PowerContext} }

// PowerSetting is a power's configured rung and the key it came from, so a
// report can say which setting to change.
type PowerSetting struct {
	Rung   string
	Source string
}

func normalise(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

func validAutonomy(s string) bool {
	switch normalise(s) {
	case AutonomyOff, AutonomyAdvise, AutonomyAsk, AutonomyAutonomous:
		return true
	}
	return false
}

// Validate rejects a coach block that names no known rung, level, or power.
func (c CoachConfig) Validate() error {
	if c.Autonomy != "" && !validAutonomy(c.Autonomy) {
		return fmt.Errorf("coach.autonomy must be off, advise, ask, or autonomous, got %q", c.Autonomy)
	}
	switch normalise(c.Verbosity) {
	case "", VerbosityQuiet, VerbosityNormal, VerbosityVerbose:
	default:
		return fmt.Errorf("coach.verbosity must be quiet, normal, or verbose, got %q", c.Verbosity)
	}
	keys := make([]string, 0, len(c.Powers))
	for k := range c.Powers {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		switch normalise(k) {
		case PowerInform, PowerWaste, PowerModels, PowerContext:
		default:
			return fmt.Errorf("coach.powers: unknown power %q (want inform, waste, models, or context)", k)
		}
		if !validAutonomy(c.Powers[k]) {
			return fmt.Errorf("coach.powers.%s must be off, advise, ask, or autonomous, got %q", k, c.Powers[k])
		}
	}
	return nil
}

// CoachPower resolves a power's rung: a per-power setting, then
// coach.autonomy, then the older keys it replaces, which keep meaning what
// they meant until the operator writes a coach block.
func (c Config) CoachPower(power string) PowerSetting {
	for k, v := range c.Coach.Powers {
		if normalise(k) == power && v != "" {
			return PowerSetting{normalise(v), "coach.powers." + power}
		}
	}
	if c.Coach.Autonomy != "" {
		return PowerSetting{normalise(c.Coach.Autonomy), "coach.autonomy"}
	}
	switch power {
	case PowerInform, PowerContext:
		// context is newer than coaching.delivery and never inherits an
		// autonomous rung from it: acting means writing the agents' own
		// settings, which only an explicit coach setting may ask for.
		if c.Coaching.DeliveryLevel() == DeliveryObserve {
			return PowerSetting{AutonomyOff, "coaching.delivery"}
		}
		return PowerSetting{AutonomyAdvise, "coaching.delivery"}
	case PowerWaste:
		switch c.Coaching.DeliveryLevel() {
		case DeliveryObserve:
			return PowerSetting{AutonomyOff, "coaching.delivery"}
		case DeliveryIntervene:
			return PowerSetting{AutonomyAutonomous, "coaching.delivery"}
		}
		return PowerSetting{AutonomyAdvise, "coaching.delivery"}
	case PowerModels:
		sr := c.Optimizer.SmartRouting
		if !sr.Enabled {
			return PowerSetting{AutonomyOff, "optimizer.smart_routing.enabled"}
		}
		switch normalise(sr.Intervention) {
		case "off", "false", "no":
			return PowerSetting{AutonomyOff, "optimizer.smart_routing.intervention"}
		case "delegate":
			return PowerSetting{AutonomyAsk, "optimizer.smart_routing.intervention"}
		case "auto":
			return PowerSetting{AutonomyAutonomous, "optimizer.smart_routing.intervention"}
		}
		return PowerSetting{AutonomyAdvise, "optimizer.smart_routing.intervention"}
	}
	return PowerSetting{AutonomyOff, "unknown power"}
}

// CoachVerbosity resolves how much the coach says and where that came from.
func (c Config) CoachVerbosity() (string, string) {
	if v := normalise(c.Coach.Verbosity); v != "" {
		return v, "coach.verbosity"
	}
	return VerbosityNormal, "default"
}
