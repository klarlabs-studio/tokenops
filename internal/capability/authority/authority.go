// Package authority answers one question: what can TokenOps do without
// being asked?
//
// Answering it used to mean reading five settings written in four
// vocabularies, and reconciling words that mean different things in
// different places — `active` most of all, which is daemon-wide live
// routing in one setting and refusing a redundant read in another. One
// of the five is not in any config file at all.
//
// This is the second capability under ADR 0004 Phase 4, and it exists
// for the same reason the first did: the CLI and the MCP server would
// otherwise each assemble this answer, and two surfaces reconciling five
// settings separately is how they come to disagree in front of an
// operator about what their own machine is allowed to do.
package authority

import (
	coachcap "go.klarlabs.de/tokenops/internal/capability/coach"
	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/contexts/policy"
)

// Subsystem is one control surface, in the common vocabulary.
type Subsystem struct {
	// Name is the subsystem, as an operator would name it.
	Name string `json:"name"`
	// Configured is the authority its own setting grants.
	Configured policy.Authority `json:"configured"`
	// Effective is what it actually has, after the daemon's cap.
	Effective policy.Authority `json:"effective"`
	// Setting names what to change to move the rung. Without it an
	// operator who wants a different answer has to go hunting again
	// through the same five places.
	Setting string `json:"setting"`
	// Describes explains what this subsystem does with its authority.
	Describes string `json:"describes"`
}

// HeldBack reports whether the daemon is capping this subsystem below
// its own setting.
//
// The distinction has to stay visible. An operator who cannot see that
// the coach is configured to intervene and is merely being held back
// will be surprised the moment they turn the daemon up.
func (s Subsystem) HeldBack() bool { return s.Effective != s.Configured }

// Answer is the whole picture.
type Answer struct {
	// Daemon is the authority the daemon grants at all, which caps
	// every subsystem below it.
	Daemon policy.Authority `json:"daemon"`
	// Subsystems are the control surfaces, in a stable order.
	Subsystems []Subsystem `json:"subsystems"`
}

// AnythingActs reports whether any subsystem may change what happens.
//
// This is the line a status surface prints. "Observes and advises" and
// "acts on its own" are the two states an operator cares about, and
// making them read a table to work out which one they are in is how the
// answer gets skipped.
func (a Answer) AnythingActs() bool {
	for _, s := range a.Subsystems {
		if s.Effective.MayAct() {
			return true
		}
	}
	return false
}

// HeldBack lists the subsystems the daemon is capping.
func (a Answer) HeldBack() []Subsystem {
	var out []Subsystem
	for _, s := range a.Subsystems {
		if s.HeldBack() {
			out = append(out, s)
		}
	}
	return out
}

// Report assembles the answer from the configuration.
//
// The order is fixed rather than sorted: it runs from the broadest
// authority to the narrowest, which is the order an operator diagnoses
// in — the daemon first, because it caps everything under it.
func Report(cfg config.Config) Answer {
	daemon := policy.FromDaemonMode(cfg.Mode)
	coach := coachcap.Build(cfg)

	// The coach's powers act inside the client through its hooks, which
	// never read the daemon's mode (ADR 0006, Decision 3): they are shown
	// at the rung they actually deliver, uncapped. Only daemon-side
	// interventions sit under the daemon's cap.
	subsystems := []Subsystem{
		coachSubsystem(coach, config.PowerInform, "coaching"),
		coachSubsystem(coach, config.PowerModels, "smart_routing"),
		coachSubsystem(coach, config.PowerWaste, "read_guard"),
		coachSubsystem(coach, config.PowerContext, "compaction"),
		{
			Name:       "routing_approval",
			Configured: policy.FromRoutingApproval(true),
			Effective:  policy.Effective(daemon, policy.FromRoutingApproval(true)),
			Setting:    "routing proposals are gated; decide them with `tokenops routing decide`",
			Describes:  "whether a proposed model route on the proxy is applied or waits for you",
		},
	}
	return Answer{Daemon: daemon, Subsystems: subsystems}
}

// coachSubsystem reports one coach power under its established name.
func coachSubsystem(r coachcap.Report, power, name string) Subsystem {
	for _, p := range r.Powers {
		if p.Name != power {
			continue
		}
		return Subsystem{
			Name:       name,
			Configured: policy.FromAutonomy(p.Configured),
			Effective:  policy.FromAutonomy(p.Effective),
			Setting:    p.Source + " (see `tokenops coach`)",
			Describes:  p.Describes,
		}
	}
	return Subsystem{Name: name, Setting: "unknown coach power"}
}
