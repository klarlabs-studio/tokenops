// Package learn projects outcome-linked experiment history into retractable
// beliefs. It never mutates runtime behavior.
package learn

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/experiments"
	"go.klarlabs.de/tokenops/internal/capability/outcomes"
	"go.klarlabs.de/tokenops/internal/contexts/learning"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// Belief is the capability-facing view of a learned routing belief.
type Belief = learning.Belief

// IsTrusted reports whether a belief has crossed the autonomy evidence gate.
func IsTrusted(b Belief) bool { return b.Tier == learning.TierTrusted }

// ExperimentReader is the read-only history needed to find a route belief.
type ExperimentReader interface {
	States(context.Context, time.Time) ([]experiments.State, error)
	Events(context.Context, string) ([]*eventschema.Envelope, error)
}

// FindRouting returns the newest belief for an exact execution fingerprint.
func FindRouting(ctx context.Context, reader ExperimentReader, fingerprint string, now time.Time) (Belief, bool, error) {
	if reader == nil || fingerprint == "" {
		return Belief{}, false, nil
	}
	states, err := reader.States(ctx, now)
	if err != nil {
		return Belief{}, false, err
	}
	var history []*eventschema.Envelope
	found := false
	for _, state := range states {
		if state.Fingerprint != fingerprint {
			continue
		}
		events, err := reader.Events(ctx, state.ID)
		if err != nil {
			return Belief{}, false, err
		}
		history = append(history, events...)
		found = true
	}
	if !found {
		return Belief{}, false, nil
	}
	return Routing(history, fingerprint, now), true, nil
}

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
	pairs := map[string]*pair{}
	byDecision := map[string]struct {
		pair string
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
		pairID := fmt.Sprintf("%s:%d", env.Correlation.Experiment, pairNo)
		eligible++
		byDecision[env.Correlation.Decision] = struct {
			pair string
			arm  string
		}{pairID, armName}
		if pairs[pairID] == nil {
			pairs[pairID] = &pair{}
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
