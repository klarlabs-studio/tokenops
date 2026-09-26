// Package learn projects outcome-linked experiment history into retractable
// beliefs. It never mutates runtime behavior.
package learn

import (
	"context"
	"fmt"
	"sort"
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
	sort.SliceStable(states, func(i, j int) bool { return states[i].EndsAt.After(states[j].EndsAt) })
	var history []*eventschema.Envelope
	found := false
	policyFingerprint := ""
	for _, state := range states {
		if state.Fingerprint != fingerprint {
			continue
		}
		candidatePolicy := experiments.UtilityPolicyFingerprint(state.ObjectiveMetric, state.MinImprovementPct, state.Guardrails)
		if !found {
			policyFingerprint = candidatePolicy
		} else if candidatePolicy != policyFingerprint {
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
	metrics  map[string]metricValue
	outcomes []*eventschema.Envelope
}

type metricValue struct {
	total float64
	count int
}

func (a *arm) add(metric string, value float64) {
	if a.metrics == nil {
		a.metrics = make(map[string]metricValue)
	}
	m := a.metrics[metric]
	m.total += value
	m.count++
	a.metrics[metric] = m
}

func (a arm) value(metric string) (float64, bool) {
	m, ok := a.metrics[metric]
	if !ok || m.count == 0 {
		return 0, false
	}
	if metric == "latency_ms" || metric == "attention_minutes" {
		return m.total / float64(m.count), true
	}
	return m.total, true
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
	objectiveMetric := ""
	minImprovementPct := 0.0
	var guardrails []learning.Guardrail
	for _, env := range events {
		if env == nil {
			continue
		}
		if p, ok := env.Payload.(*eventschema.ExperimentEvent); ok && p.Stage == eventschema.ExperimentStarted {
			objectiveMetric, minImprovementPct = p.ObjectiveMetric, p.MinImprovementPct
			guardrails = make([]learning.Guardrail, 0, len(p.Guardrails))
			for _, g := range p.Guardrails {
				guardrails = append(guardrails, learning.Guardrail{Metric: g.Metric, MaxRegressionPct: g.MaxRegressionPct})
			}
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
				a.add("tokens", float64(payload.TotalTokens))
				if payload.CostSource == eventschema.CostSourcePlanIncluded {
					a.add("plan_quota_tokens", float64(payload.TotalTokens))
				}
			}
			if payload.CostMeasured && (payload.CostSource == "" || payload.CostSource == eventschema.CostSourceMetered) {
				a.add("metered_cost_usd", payload.CostUSD)
			}
			if payload.Latency > 0 {
				a.add("latency_ms", float64(payload.Latency)/float64(time.Millisecond))
			}
		case *eventschema.OutcomeEvent:
			a.outcomes = append(a.outcomes, env)
			for _, metric := range payload.Metrics {
				if metric.Name == "human_attention_minutes" && metric.Unit == "minutes" && metric.Source == "human_self_report" && metric.Value >= 0 {
					a.add("attention_minutes", metric.Value)
				}
			}
		}
	}
	var evidence []learning.Pair
	for _, p := range pairs {
		if len(p.baseline.outcomes) == 0 || len(p.variant.outcomes) == 0 {
			continue
		}
		metrics := make(map[string]learning.MetricPair)
		for metric, baseline := range p.baseline.metrics {
			variant, ok := p.variant.metrics[metric]
			if !ok || baseline.count == 0 || variant.count == 0 {
				continue
			}
			baselineValue, baselineOK := p.baseline.value(metric)
			variantValue, variantOK := p.variant.value(metric)
			if baselineOK && variantOK {
				metrics[metric] = learning.MetricPair{Baseline: baselineValue, Variant: variantValue}
			}
		}
		change := 0.0
		if values, ok := metrics[objectiveMetric]; ok {
			if values.Baseline > 0 {
				change = 100 * (values.Baseline - values.Variant) / values.Baseline
			} else if values.Variant > 0 {
				change = -100
			}
		}
		at := p.baseline.at
		if p.variant.at.After(at) {
			at = p.variant.at
		}
		evidence = append(evidence, learning.Pair{
			Baseline: outcomes.Resolve(p.baseline.outcomes), Variant: outcomes.Resolve(p.variant.outcomes),
			ResourceChangePct: change, Metrics: metrics, At: at, Fingerprint: fingerprint,
		})
	}
	return learning.Evaluate(learning.Evidence{
		Eligible: eligible, Pairs: evidence, Fingerprint: fingerprint, Now: now,
		ObjectiveMetric: objectiveMetric, MinImprovementPct: minImprovementPct, Guardrails: guardrails,
	})
}
