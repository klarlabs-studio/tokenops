package config

import "strings"

// OptimizerMode says what the optimizer is allowed to do with a request.
//
// This used to be two states buried inside the daemon-wide mode: active
// applied every rule, passive did nothing. There was no way to say
// "propose it and let me decide", even though the preferred-model ceiling
// already worked exactly that way for upgrades. Three named modes make
// the middle ground the default shape rather than a special case.
type OptimizerMode string

// Optimizer modes.
const (
	// OptimizerAutomatic rewrites matching requests in flight. The
	// operator sees the decision afterwards, in the optimization events.
	OptimizerAutomatic OptimizerMode = "automatic"
	// OptimizerInRequest refuses to rewrite anything on its own and
	// records a proposal instead, which the operator answers through the
	// MCP surface. The request goes upstream exactly as the client sent
	// it, because an in-flight HTTP request cannot wait for a human.
	OptimizerInRequest OptimizerMode = "in_request"
	// OptimizerOff observes: rules are evaluated and what they would have
	// done is recorded, but nothing is changed and nothing is asked.
	OptimizerOff OptimizerMode = "off"
)

// Applies reports whether the optimizer may rewrite a request unasked.
func (m OptimizerMode) Applies() bool {
	return strings.EqualFold(string(m), string(OptimizerAutomatic))
}

// Proposes reports whether the optimizer should refer decisions to the
// operator rather than making them.
func (m OptimizerMode) Proposes() bool {
	return strings.EqualFold(string(m), string(OptimizerInRequest))
}

// ObserveOnly reports whether the optimizer must leave requests alone.
//
// The empty value lands here on purpose. An operator who upgrades should
// not discover afterwards that their model started being swapped; opting
// into an intervention has to be an act, not a default.
func (m OptimizerMode) ObserveOnly() bool {
	return !m.Applies() && !m.Proposes()
}

// Valid reports whether the mode is one this build understands.
func (m OptimizerMode) Valid() bool {
	switch strings.ToLower(strings.TrimSpace(string(m))) {
	case "", string(OptimizerAutomatic), string(OptimizerInRequest), string(OptimizerOff):
		return true
	default:
		return false
	}
}
