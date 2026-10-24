package config

import (
	"strings"

	"go.klarlabs.de/tokenops/internal/contexts/boundaries"
)

// HeadroomPauseName is the name of the boundary `tokenops init` writes on
// a new config: the headroom pause.
const HeadroomPauseName = "headroom-pause"

// headroomPauseAt is the share of the tightest plan window left at which
// the headroom pause applies: 10% left. It leaves room for an agent to
// finish the step it is on, commit and hand over, and it is where the
// one-feature tools that sell this pause put it.
const headroomPauseAt = "<= 10"

// HeadroomPauseRule is the headroom pause as init writes it: the agent's
// next tool call, once the tightest plan window TokenOps knows is down to
// 10% left, asks first. It starts in shadow, as every boundary does (ADR
// 0014): it records each session it would have paused and binds nothing
// until `tokenops boundaries promote headroom-pause`.
func HeadroomPauseRule() boundaries.RuleSpec {
	return boundaries.RuleSpec{
		Name:    HeadroomPauseName,
		Action:  string(boundaries.ActionContinue),
		When:    map[string]string{string(boundaries.FieldPlanLeft): headroomPauseAt},
		Require: string(boundaries.RequireAsk),
		Stage:   string(boundaries.StageShadow),
	}
}

// HasHeadroomPause reports whether the config's own boundaries pause the
// agent on plan headroom: a continue rule on plan_left_pct, whatever it
// is called. The operator's rule counts as much as the default.
func (c Config) HasHeadroomPause() bool {
	for _, s := range c.Boundaries {
		if !strings.EqualFold(strings.TrimSpace(s.Action), string(boundaries.ActionContinue)) {
			continue
		}
		if _, ok := s.When[string(boundaries.FieldPlanLeft)]; ok {
			return true
		}
	}
	return false
}
