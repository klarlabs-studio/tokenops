package config

import (
	"fmt"
	"sort"
	"strings"
	"time"
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
	// Holdout is the percent of sessions whose cost and drift tips are
	// held back and logged as shadow, so the realized saving of a tip can
	// be measured against sessions that did not get it (#656), and the
	// percent of the subagent moves the coach would make on its own that
	// it holds back, so each kind of move is judged against work chance
	// left on the model it came from. Unset is
	// DefaultHoldoutPercent; 0 opts out; at most MaxHoldoutPercent. Tips
	// about work about to stop are never held. A preset keeps it.
	Holdout *int `yaml:"holdout,omitempty"`
	// HeadroomNote puts a one-line plan headroom note in front of the
	// agent through the per-turn prompt hook (route-guard), once per
	// session and again when a window crosses 50, 25 or 10% left. Unset
	// is on; false turns it off. It never enters a session's cached
	// prefix. The inform power being off also silences it.
	HeadroomNote *bool `yaml:"headroom_note,omitempty"`
	// Tips bounds the tips the coach gives the agent at decision points:
	// a tool call, a subagent launch, the per-turn model advice and
	// tokenops_prepare_work.
	Tips CoachTipsConfig `yaml:"tips,omitempty"`
}

// CoachTipsConfig is the decision-point tip throttle.
type CoachTipsConfig struct {
	// PerHour is the most decision-point tips one session is given in
	// any hour. Unset is DefaultCoachTipsPerHour; 1 to MaxCoachTipsPerHour.
	PerHour *int `yaml:"per_hour,omitempty"`
	// Cooldown is how long the same tip (its kind and the one thing it
	// names) waits before it is given again in a session, as a Go
	// duration ("1h", "30m"). Unset is DefaultCoachTipCooldown; at least
	// a minute.
	Cooldown string `yaml:"cooldown,omitempty"`
}

// The decision-point tip throttle's defaults and bounds.
const (
	DefaultCoachTipsPerHour = 6
	MaxCoachTipsPerHour     = 60
	DefaultCoachTipCooldown = time.Hour
)

// CoachTipThrottle is the decision-point tip throttle in effect: tips per
// session per hour and the cool-down before the same tip is said again.
// An invalid value reads as its default; Validate rejects it on write.
func (c Config) CoachTipThrottle() (perHour int, cooldown time.Duration) {
	perHour, cooldown = DefaultCoachTipsPerHour, DefaultCoachTipCooldown
	if p := c.Coach.Tips.PerHour; p != nil && *p >= 1 && *p <= MaxCoachTipsPerHour {
		perHour = *p
	}
	if d, err := time.ParseDuration(strings.TrimSpace(c.Coach.Tips.Cooldown)); err == nil && d >= time.Minute {
		cooldown = d
	}
	return perHour, cooldown
}

// CoachHeadroomNote reports whether the headroom note is on.
func (c Config) CoachHeadroomNote() bool {
	return c.Coach.HeadroomNote == nil || *c.Coach.HeadroomNote
}

// The holdout's bounds: on by default, and small, because it withholds
// help.
const (
	DefaultHoldoutPercent = 10
	MaxHoldoutPercent     = 10
)

// CoachHoldoutPercent is the holdout share in effect.
func (c Config) CoachHoldoutPercent() int {
	if c.Coach.Holdout == nil {
		return DefaultHoldoutPercent
	}
	return min(max(*c.Coach.Holdout, 0), MaxHoldoutPercent)
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
	if c.Holdout != nil && (*c.Holdout < 0 || *c.Holdout > MaxHoldoutPercent) {
		return fmt.Errorf("coach.holdout must be 0 (off) to %d percent, got %d", MaxHoldoutPercent, *c.Holdout)
	}
	if p := c.Tips.PerHour; p != nil && (*p < 1 || *p > MaxCoachTipsPerHour) {
		return fmt.Errorf("coach.tips.per_hour must be 1 to %d, got %d", MaxCoachTipsPerHour, *p)
	}
	if cd := strings.TrimSpace(c.Tips.Cooldown); cd != "" {
		if d, err := time.ParseDuration(cd); err != nil || d < time.Minute {
			return fmt.Errorf("coach.tips.cooldown must be a duration of at least 1m, such as 30m or 2h, got %q", c.Tips.Cooldown)
		}
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
