package config

import (
	"strings"

	"go.klarlabs.de/tokenops/internal/contexts/boundaries"
)

// LoopBreakerName is the name of the loop breaker `tokenops init` writes
// on a new config.
const LoopBreakerName = "loop-breaker"

// loopBreakerAt is how many calls round a loop with no file changed the
// loop breaker lets go before it applies: the same call four times, or a
// cycle of two or three calls three times (each at least four calls).
const loopBreakerAt = ">= 4"

// LoopBreakerRule is the loop breaker as init writes it: once the agent's
// latest tool calls go round a loop with no file changed, the next call
// asks first. It starts in shadow, as every boundary does (ADR 0014): it
// records each session it would have paused and binds nothing until
// `tokenops boundaries promote loop-breaker`.
func LoopBreakerRule() boundaries.RuleSpec {
	return boundaries.RuleSpec{
		Name:    LoopBreakerName,
		Action:  string(boundaries.ActionContinue),
		When:    map[string]string{string(boundaries.FieldLoopCalls): loopBreakerAt},
		Require: string(boundaries.RequireAsk),
		Stage:   string(boundaries.StageShadow),
	}
}

// HasLoopBreaker reports whether the config's own boundaries break loops:
// a continue rule on loop_calls, whatever it is called.
func (c Config) HasLoopBreaker() bool {
	for _, s := range c.Boundaries {
		if !strings.EqualFold(strings.TrimSpace(s.Action), string(boundaries.ActionContinue)) {
			continue
		}
		if _, ok := s.When[string(boundaries.FieldLoopCalls)]; ok {
			return true
		}
	}
	return false
}
