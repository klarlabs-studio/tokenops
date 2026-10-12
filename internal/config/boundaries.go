package config

import "go.klarlabs.de/tokenops/internal/contexts/boundaries"

// BoundaryRules are the boundaries section in the domain's form (ADR
// 0014). Load has already validated it, so a parse error here means the
// Config was built without Load; it yields no rules, which binds nothing.
func (c Config) BoundaryRules() []boundaries.Rule {
	rules, err := boundaries.Parse(c.Boundaries)
	if err != nil {
		return nil
	}
	return rules
}
