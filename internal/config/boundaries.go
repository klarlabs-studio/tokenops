package config

import (
	"go.klarlabs.de/tokenops/internal/contexts/boundaries"
	"go.klarlabs.de/tokenops/internal/contexts/boundaries/presence"
)

// BoundaryRules are the boundaries section and the hard budget windows'
// rules, in the domain's form (ADR
// 0014). Load has already validated it, so a parse error here means the
// Config was built without Load; it yields no rules, which binds nothing.
func (c Config) BoundaryRules() []boundaries.Rule {
	rules, err := boundaries.Parse(c.boundarySpecs())
	if err != nil {
		return nil
	}
	return rules
}

// ApprovalMode is how approvals are checked (boundary_approval). Load has
// already validated it; a value that does not parse is the default,
// channel, which is what a Config built without Load had before.
func (c Config) ApprovalMode() presence.Mode {
	m, err := presence.ParseMode(c.BoundaryApproval)
	if err != nil {
		return presence.ModeChannel
	}
	return m
}
