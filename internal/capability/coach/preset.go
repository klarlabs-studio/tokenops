package coach

import (
	"maps"
	"strings"
	"time"

	"go.klarlabs.de/tokenops/internal/config"
)

// Preset is a whole coach in one choice: each power's autonomy and how
// much the coach says, chosen to fit together. Applying one also installs
// every hook the coach needs and sets where agents compact, so an
// operator (or an agent asked to configure tokenops) never assembles
// those pieces by hand.
type Preset struct {
	Name    string `json:"name"`
	Summary string `json:"summary"`
	// Coach is the coach block the preset writes. It replaces the block
	// wholesale: a preset is a known state, not an overlay.
	Coach config.CoachConfig `json:"coach"`
}

// PresetDefault is the preset for someone who has not chosen: the coach
// tells, and changes nothing.
const PresetDefault = "advise"

// Presets lists the presets from least to most autonomous.
func Presets() []Preset {
	return []Preset{
		{
			Name:    "observe",
			Summary: "records what the coach would say or do, and says and does nothing",
			Coach:   config.CoachConfig{Autonomy: config.AutonomyOff, Verbosity: config.VerbosityQuiet},
		},
		{
			Name:    "advise",
			Summary: "tells you what you could do better, once per kind of thing; changes nothing",
			Coach:   config.CoachConfig{Autonomy: config.AutonomyAdvise, Verbosity: config.VerbosityNormal},
		},
		{
			Name: "guided",
			Summary: "refuses redundant re-reads and asks before moving subagents to a cheaper model; " +
				"advises on everything else",
			Coach: config.CoachConfig{
				Autonomy: config.AutonomyAdvise, Verbosity: config.VerbosityNormal,
				Powers: map[string]string{config.PowerWaste: config.AutonomyAutonomous, config.PowerModels: config.AutonomyAsk},
			},
		},
		{
			Name: "autopilot",
			Summary: "acts on its own and stays quiet: refuses redundant re-reads, moves subagents to a cheaper model, " +
				"and sets where each agent compacts",
			Coach: config.CoachConfig{Autonomy: config.AutonomyAutonomous, Verbosity: config.VerbosityQuiet},
		},
	}
}

// PresetByName finds a preset, ignoring case.
func PresetByName(name string) (Preset, bool) {
	for _, p := range Presets() {
		if strings.EqualFold(p.Name, strings.TrimSpace(name)) {
			return p, true
		}
	}
	return Preset{}, false
}

// PresetNames lists the names, for help text and errors.
func PresetNames() []string {
	ps := Presets()
	out := make([]string, 0, len(ps))
	for _, p := range ps {
		out = append(out, p.Name)
	}
	return out
}

// CurrentPreset names the preset the configuration matches power for
// power, or "" when it has been tuned away from all of them. Compared on
// the resolved rungs, so an explicit override that repeats the default
// still matches.
func CurrentPreset(cfg config.Config) string {
	for _, p := range Presets() {
		candidate := cfg
		candidate.Coach = p.Coach
		if samePowers(cfg, candidate) {
			return p.Name
		}
	}
	return ""
}

func samePowers(a, b config.Config) bool {
	for _, power := range config.Powers() {
		if a.CoachPower(power).Rung != b.CoachPower(power).Rung {
			return false
		}
	}
	av, _ := a.CoachVerbosity()
	bv, _ := b.CoachVerbosity()
	return av == bv
}

// ApplyPreset writes a preset's coach block through Apply, so lowering a
// power records it and the context power's agent settings follow.
func ApplyPreset(path string, l Ledger, levers ContextLevers, p Preset, now time.Time) (Report, error) {
	return Apply(path, l, levers, now, func(c *config.Config) {
		c.Coach = config.CoachConfig{
			Autonomy: p.Coach.Autonomy, Verbosity: p.Coach.Verbosity,
			Powers: maps.Clone(p.Coach.Powers),
		}
	})
}
