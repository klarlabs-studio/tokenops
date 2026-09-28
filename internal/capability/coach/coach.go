// Package coach reports the coach as one capability (ADR 0006): each
// power's configured autonomy, what it can actually do on this machine,
// why the two differ, and the setting it came from. The CLI, the MCP
// server, and the hooks all read it, so what the coach says it does and
// what it does cannot drift apart.
package coach

import (
	"go.klarlabs.de/tokenops/internal/config"
	ft "go.klarlabs.de/tokenops/internal/contexts/coaching/followthrough"
)

// Power is one of the coach's powers as the operator sees it.
type Power struct {
	Name       string `json:"name"`
	Configured string `json:"configured"`
	// Effective is the rung the coach delivers here. It is lower than
	// Configured when a rung is not available yet, and Reason says why.
	Effective string `json:"effective"`
	Reason    string `json:"reason,omitempty"`
	// Note qualifies what the effective rung covers here.
	Note string `json:"note,omitempty"`
	// Source is the setting the configured rung came from.
	Source    string `json:"source"`
	Describes string `json:"describes"`
}

// Report is the whole coach.
type Report struct {
	Powers          []Power `json:"powers"`
	Verbosity       string  `json:"verbosity"`
	VerbositySource string  `json:"verbosity_source"`
	// FollowThrough is what became of the coach's interventions, per
	// kind (ADR 0006, decision 6). Only Status fills it.
	FollowThrough []ft.Summary `json:"follow_through,omitempty"`
}

// Off reports whether every power is off: the coach records and says
// nothing.
func (r Report) Off() bool {
	for _, p := range r.Powers {
		if p.Effective != config.AutonomyOff {
			return false
		}
	}
	return true
}

// Effective returns the effective rung of one power.
func (r Report) Effective(power string) string {
	for _, p := range r.Powers {
		if p.Name == power {
			return p.Effective
		}
	}
	return config.AutonomyOff
}

var describes = map[string]string{
	config.PowerInform: "tips on the plan's quota window and context fullness",
	config.PowerWaste:  "redundant re-reads of unchanged files",
	config.PowerModels: "moving work to a cheaper model that fits it",
}

// Build resolves every power from cfg.
func Build(cfg config.Config) Report {
	r := Report{}
	r.Verbosity, r.VerbositySource = cfg.CoachVerbosity()
	for _, name := range config.Powers() {
		set := cfg.CoachPower(name)
		eff, why := effective(name, set.Rung)
		p := Power{
			Name: name, Configured: set.Rung, Effective: eff, Reason: why,
			Source: set.Source, Describes: describes[name],
		}
		switch {
		case name == config.PowerModels && eff == config.AutonomyAutonomous:
			p.Note = "moves subagents to a cheaper model on Claude Code; the session's own model is advised, not changed"
		case name == config.PowerModels && eff == config.AutonomyAsk:
			p.Note = "asks before moving a subagent to a cheaper model in interactive Claude Code sessions, and advises when nobody is attending; " +
				"declining pauses the agent, and telling it to continue runs the subagent as planned"
		}
		r.Powers = append(r.Powers, p)
	}
	return r
}

// effective is the rung each power can deliver today.
func effective(power, rung string) (string, string) {
	switch rung {
	case config.AutonomyOff, config.AutonomyAdvise:
		return rung, ""
	}
	switch power {
	case config.PowerInform:
		return config.AutonomyAdvise, "the coach informs by advising; changes happen through waste and models"
	case config.PowerWaste:
		if rung == config.AutonomyAutonomous {
			return rung, ""
		}
		return config.AutonomyAdvise, "the read guard has no approval step: declining a refused re-read would stop the agent's turn, so it advises instead"
	case config.PowerModels:
		return rung, ""
	}
	return config.AutonomyOff, "unknown power"
}
