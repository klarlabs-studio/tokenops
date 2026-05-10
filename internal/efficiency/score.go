// Package efficiency computes a composite efficiency score per
// workflow / agent / user. The score is a weighted aggregate of waste
// signals derived from a workflow.Trace: context discipline (peak +
// growth), output-to-input ratio (high output dominates is healthy;
// agents that read 50× what they produce are coasting), recursion /
// agent-loop frequency, and cache reuse.
//
// All component scores are normalised to [0,1] before being weighted.
// The composite score is then exposed as a 0–100 grade for dashboards.
package efficiency

import (
	"math"

	"github.com/felixgeelhaar/tokenops/internal/workflow"
)

// Weights tunes the component contributions. Defaults sum to 1.0; pass
// custom weights to bias the score toward specific concerns. Zero
// values fall back to the defaults below.
type Weights struct {
	Context   float64 // context discipline (peak + growth)
	IORatio   float64 // output / (input+output) ratio
	Loops     float64 // anti-loop bonus
	Diversity float64 // agent / model diversity
}

// DefaultWeights is the production-tested baseline: context discipline
// dominates (40%) because that is the main cost driver, balanced by IO
// ratio (30%), loops (20%), and diversity (10%).
func DefaultWeights() Weights {
	return Weights{Context: 0.4, IORatio: 0.3, Loops: 0.2, Diversity: 0.1}
}

func (w Weights) normalised() Weights {
	if w.Context+w.IORatio+w.Loops+w.Diversity <= 0 {
		return DefaultWeights()
	}
	sum := w.Context + w.IORatio + w.Loops + w.Diversity
	return Weights{
		Context:   w.Context / sum,
		IORatio:   w.IORatio / sum,
		Loops:     w.Loops / sum,
		Diversity: w.Diversity / sum,
	}
}

// Score is the breakdown of a single efficiency evaluation. Each
// component is in [0,1]; Total is in [0,1] and Grade is in [0,100].
type Score struct {
	Total     float64
	Grade     int
	Context   float64
	IORatio   float64
	Loops     float64
	Diversity float64
}

// Config tunes component-score thresholds. Zero values produce
// sensible defaults.
type Config struct {
	// SoftContextLimit is the InputTokens value at which context score
	// equals 0.5; the score follows a soft sigmoid around it. Default
	// 16_384.
	SoftContextLimit int64
	// MaxAgentLoopRun is the longest acceptable consecutive same-agent
	// run; longer runs reduce the loop component. Default 4.
	MaxAgentLoopRun int
}

func (c *Config) defaults() {
	if c.SoftContextLimit <= 0 {
		c.SoftContextLimit = 16_384
	}
	if c.MaxAgentLoopRun <= 0 {
		c.MaxAgentLoopRun = 4
	}
}

// Evaluate computes the Score for a workflow trace. nil/empty traces
// return a zero Score (Total 0, Grade 0).
func Evaluate(trace *workflow.Trace, cfg Config, weights Weights) Score {
	if trace == nil || trace.StepCount == 0 {
		return Score{}
	}
	cfg.defaults()
	w := weights.normalised()

	context := contextScore(trace, cfg)
	io := ioRatio(trace)
	loops := loopScore(trace, cfg)
	diversity := diversityScore(trace)

	total := context*w.Context + io*w.IORatio + loops*w.Loops + diversity*w.Diversity
	if total > 1 {
		total = 1
	}
	if total < 0 {
		total = 0
	}
	return Score{
		Total:     total,
		Grade:     int(math.Round(total * 100)),
		Context:   context,
		IORatio:   io,
		Loops:     loops,
		Diversity: diversity,
	}
}

func contextScore(t *workflow.Trace, cfg Config) float64 {
	// Soft sigmoid: 0.5 at SoftContextLimit, decays toward 0 above it.
	x := float64(t.MaxContextSize) / float64(cfg.SoftContextLimit)
	score := 1 / (1 + x*x)
	// Penalise runaway growth: subtract up to 0.3 when total positive
	// growth equals the soft limit.
	growthPenalty := math.Min(0.3, float64(t.ContextGrowthTotal)/float64(cfg.SoftContextLimit)*0.3)
	score -= growthPenalty
	if score < 0 {
		return 0
	}
	if score > 1 {
		return 1
	}
	return score
}

func ioRatio(t *workflow.Trace) float64 {
	total := float64(t.TotalInputTokens + t.TotalOutputTokens)
	if total == 0 {
		return 0
	}
	ratio := float64(t.TotalOutputTokens) / total
	// Healthy agents emit 20–40% of their token budget as output. Map
	// 0% → 0, 30% → 1, anything above → 1 minus a small gentle decay
	// so monologues do not score perfectly either.
	switch {
	case ratio >= 0.5:
		return math.Max(0.7, 1-(ratio-0.3)*0.4)
	case ratio >= 0.2:
		return 1 - math.Abs(ratio-0.3)/0.1*0.05
	default:
		return ratio / 0.2
	}
}

func loopScore(t *workflow.Trace, cfg Config) float64 {
	maxRun := longestAgentRun(t)
	if maxRun <= cfg.MaxAgentLoopRun {
		return 1
	}
	// Linear penalty beyond the limit, clamped at 0 once the run is
	// 3× the threshold (clear runaway loop).
	excess := float64(maxRun - cfg.MaxAgentLoopRun)
	span := float64(cfg.MaxAgentLoopRun) * 2
	score := 1 - excess/span
	if score < 0 {
		return 0
	}
	return score
}

func longestAgentRun(t *workflow.Trace) int {
	var (
		longest int
		curRun  int
		curAg   string
	)
	for _, step := range t.Steps {
		ag := step.Prompt.AgentID
		if ag == "" {
			curRun = 0
			curAg = ""
			continue
		}
		if ag == curAg {
			curRun++
		} else {
			curAg = ag
			curRun = 1
		}
		if curRun > longest {
			longest = curRun
		}
	}
	return longest
}

// diversityScore rewards workflows that exercise multiple models /
// agents — a one-model one-agent workflow is fine, but the score
// penalises over-reliance on a single agent for very long runs.
func diversityScore(t *workflow.Trace) float64 {
	if t.StepCount == 0 {
		return 0
	}
	// Long workflows with one agent score lower; short ones are fine.
	if t.StepCount <= 4 {
		return 1
	}
	uniqueAgents := len(t.Agents)
	uniqueModels := len(t.Models)
	if uniqueAgents == 0 {
		uniqueAgents = 1
	}
	if uniqueModels == 0 {
		uniqueModels = 1
	}
	agentRatio := math.Min(1, float64(uniqueAgents)/3.0)
	modelRatio := math.Min(1, float64(uniqueModels)/2.0)
	return (agentRatio + modelRatio) / 2
}
