// Package learn projects outcome-linked experiment history into retractable
// beliefs. It never mutates runtime behavior.
package learn

import (
	"strconv"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/outcomes"
	"go.klarlabs.de/tokenops/internal/contexts/learning"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// Belief is the capability-facing view of a learned routing belief.
type Belief = learning.Belief

type arm struct {
	at       time.Time
	tokens   float64
	counted  bool
	outcomes []*eventschema.Envelope
}

type pair struct{ baseline, variant arm }

// Routing derives a belief for one experiment from decision, prompt and
// outcome events carrying its correlation id.
func Routing(events []*eventschema.Envelope, fingerprint string, now time.Time) Belief {
	pairs := map[int]*pair{}
	byDecision := map[string]struct {
		pair int
		arm  string
	}{}
	eligible := 0
	for _, env := range events {
		if env == nil {
			continue
		}
		if _, ok := env.Payload.(*eventschema.DecisionEvent); !ok {
			continue
		}
		pairNo, _ := strconv.Atoi(env.Attributes["tokenops.experiment.pair"])
		armName := env.Attributes["tokenops.experiment.arm"]
		if pairNo < 1 || (armName != "baseline" && armName != "variant") {
			continue
		}
		eligible++
		byDecision[env.Correlation.Decision] = struct {
			pair int
			arm  string
		}{pairNo, armName}
		if pairs[pairNo] == nil {
			pairs[pairNo] = &pair{}
		}
	}
	for _, env := range events {
		if env == nil {
			continue
		}
		key, ok := byDecision[env.Correlation.Decision]
		if !ok {
			continue
		}
		p := pairs[key.pair]
		a := &p.baseline
		if key.arm == "variant" {
			a = &p.variant
		}
		if env.Timestamp.After(a.at) {
			a.at = env.Timestamp
		}
		switch payload := env.Payload.(type) {
		case *eventschema.PromptEvent:
			if payload.TokensCounted() {
				a.tokens += float64(payload.TotalTokens)
				a.counted = true
			}
		case *eventschema.OutcomeEvent:
			a.outcomes = append(a.outcomes, env)
		}
	}
	var evidence []learning.Pair
	for _, p := range pairs {
		if !p.baseline.counted || !p.variant.counted || len(p.baseline.outcomes) == 0 || len(p.variant.outcomes) == 0 {
			continue
		}
		change := 0.0
		if p.baseline.tokens > 0 {
			change = 100 * (p.baseline.tokens - p.variant.tokens) / p.baseline.tokens
		}
		at := p.baseline.at
		if p.variant.at.After(at) {
			at = p.variant.at
		}
		evidence = append(evidence, learning.Pair{
			Baseline: outcomes.Resolve(p.baseline.outcomes), Variant: outcomes.Resolve(p.variant.outcomes),
			ResourceChangePct: change, At: at, Fingerprint: fingerprint,
		})
	}
	return learning.Evaluate(learning.Evidence{
		Eligible: eligible, Pairs: evidence, Fingerprint: fingerprint, Now: now,
	})
}
