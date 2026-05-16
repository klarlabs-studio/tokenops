// Package waste scans reconstructed workflow traces for known waste
// patterns — oversized context, runaway context growth, redundant system
// prompts, repeated agent loops, and prompt-hash recursion. Each finding
// is returned as an eventschema.CoachingEvent so the CLI / dashboard can
// surface concrete recommendations alongside the workflow timeline.
//
// The detector is a pure read+compute layer. It never writes back to the
// store — coaching events are returned to the caller (replay engine,
// async pipeline) which decides whether to persist them.
package waste

import (
	"fmt"
	"strings"

	"github.com/felixgeelhaar/tokenops/internal/contexts/workflows/workflow"
	"github.com/felixgeelhaar/tokenops/pkg/eventschema"
)

// Config tunes the detector. Zero values produce reasonable defaults
// derived from common production agent runs.
type Config struct {
	// MaxContextTokens is the absolute ceiling above which a step's
	// InputTokens triggers an OversizedContext finding. Default 32_768.
	MaxContextTokens int64
	// ContextGrowthLimitTokens flags a workflow whose total positive
	// context growth exceeds this number. Default 16_384.
	ContextGrowthLimitTokens int64
	// MaxConsecutiveAgentLoops is the threshold for the
	// RepeatedAgentLoops pattern (a single agent firing N+ times in a
	// row, often a sign of an unbounded retry / re-plan loop). Default 4.
	MaxConsecutiveAgentLoops int
	// SystemRedundancyMin is the minimum repeated system-prompt hash
	// occurrences before flagging RedundantSystemPrompt. Default 3 — we
	// expect every step to ship the system prompt; coaching kicks in
	// only when the operator could obviously hoist it via the
	// system-dedupe optimizer.
	SystemRedundancyMin int
}

func (c *Config) defaults() {
	if c.MaxContextTokens <= 0 {
		c.MaxContextTokens = 32_768
	}
	if c.ContextGrowthLimitTokens <= 0 {
		c.ContextGrowthLimitTokens = 16_384
	}
	if c.MaxConsecutiveAgentLoops <= 0 {
		c.MaxConsecutiveAgentLoops = 4
	}
	if c.SystemRedundancyMin <= 0 {
		c.SystemRedundancyMin = 3
	}
}

// Detector scans a workflow.Trace and emits coaching events.
type Detector struct {
	cfg Config
}

// New constructs a Detector with cfg (zero values backfilled).
func New(cfg Config) *Detector {
	cfg.defaults()
	return &Detector{cfg: cfg}
}

// Detect runs all patterns over the trace and returns a slice of
// coaching event envelopes. The envelope IDs are not assigned here —
// the caller (replay engine, async pipeline) is responsible for ID
// minting + storage. Thresholds adapt to the workflow profile when
// the operator's Config is the zero default — code-agent sessions
// (workflow_id prefix "claude-code:") run 1M context caps and would
// trip the short-workflow thresholds on every session.
func (d *Detector) Detect(trace *workflow.Trace) []*eventschema.CoachingEvent {
	if trace == nil || len(trace.Steps) == 0 {
		return nil
	}
	cfg := d.cfg
	if profile := ProfileFor(trace.WorkflowID); profile != nil {
		cfg = mergeConfig(cfg, *profile)
	}
	scoped := &Detector{cfg: cfg}
	var out []*eventschema.CoachingEvent
	if ev := scoped.checkOversizedContext(trace); ev != nil {
		out = append(out, ev)
	}
	if ev := scoped.checkContextGrowth(trace); ev != nil {
		out = append(out, ev)
	}
	if ev := scoped.checkAgentLoops(trace); ev != nil {
		out = append(out, ev)
	}
	if ev := scoped.checkRecursion(trace); ev != nil {
		out = append(out, ev)
	}
	return out
}

// ProfileFor returns a Config tuned to a workflow's expected shape,
// or nil when the default short-workflow thresholds apply. Two
// code-agent profiles ship today:
//
//   - "claude-code:" — Anthropic's claude-opus-4-7 caps at 1M
//     context; flag only at ~90% of the ceiling.
//   - "codex:" — OpenAI Codex CLI runs ~256k context windows on
//     gpt-5 family models; flag only near 250k peak.
//
// Both profiles also raise the cumulative growth limit so legitimate
// long-running agent sessions don't trip on every run.
func ProfileFor(workflowID string) *Config {
	switch {
	case strings.HasPrefix(workflowID, "claude-code:"):
		return &Config{
			MaxContextTokens:         900_000,
			ContextGrowthLimitTokens: 2_000_000,
		}
	case strings.HasPrefix(workflowID, "codex:"):
		return &Config{
			MaxContextTokens:         250_000,
			ContextGrowthLimitTokens: 500_000,
		}
	}
	return nil
}

// mergeConfig overlays profile onto base, preferring non-zero
// profile values. Zero fields in profile fall back to base, which
// preserves any operator-supplied tuning.
func mergeConfig(base, profile Config) Config {
	out := base
	if profile.MaxContextTokens > 0 {
		out.MaxContextTokens = profile.MaxContextTokens
	}
	if profile.ContextGrowthLimitTokens > 0 {
		out.ContextGrowthLimitTokens = profile.ContextGrowthLimitTokens
	}
	if profile.MaxConsecutiveAgentLoops > 0 {
		out.MaxConsecutiveAgentLoops = profile.MaxConsecutiveAgentLoops
	}
	if profile.SystemRedundancyMin > 0 {
		out.SystemRedundancyMin = profile.SystemRedundancyMin
	}
	return out
}

func (d *Detector) checkOversizedContext(t *workflow.Trace) *eventschema.CoachingEvent {
	if t.MaxContextSize < d.cfg.MaxContextTokens {
		return nil
	}
	return &eventschema.CoachingEvent{
		WorkflowID: t.WorkflowID,
		Kind:       eventschema.CoachingKindTrimContext,
		Summary:    "Oversized context window",
		Details: fmt.Sprintf(
			"Workflow peak context = %d tokens (limit %d). Consider trimming older turns or summarising.",
			t.MaxContextSize, d.cfg.MaxContextTokens),
		EstimatedSavingsTokens: t.MaxContextSize - d.cfg.MaxContextTokens,
	}
}

func (d *Detector) checkContextGrowth(t *workflow.Trace) *eventschema.CoachingEvent {
	if t.ContextGrowthTotal < d.cfg.ContextGrowthLimitTokens {
		return nil
	}
	return &eventschema.CoachingEvent{
		WorkflowID: t.WorkflowID,
		Kind:       eventschema.CoachingKindTrimContext,
		Summary:    "Runaway context growth",
		Details: fmt.Sprintf(
			"Cumulative context growth = %d tokens across %d steps (limit %d). Each step is appending more than it should.",
			t.ContextGrowthTotal, t.StepCount, d.cfg.ContextGrowthLimitTokens),
		EstimatedSavingsTokens: t.ContextGrowthTotal - d.cfg.ContextGrowthLimitTokens,
	}
}

func (d *Detector) checkAgentLoops(t *workflow.Trace) *eventschema.CoachingEvent {
	runLen, agent := longestAgentRun(t)
	if runLen < d.cfg.MaxConsecutiveAgentLoops {
		return nil
	}
	return &eventschema.CoachingEvent{
		WorkflowID: t.WorkflowID,
		AgentID:    agent,
		Kind:       eventschema.CoachingKindBreakRecursion,
		Summary:    "Repeated agent loop detected",
		Details: fmt.Sprintf(
			"Agent %q invoked %d times consecutively (limit %d). Likely an unbounded retry or re-plan loop.",
			agent, runLen, d.cfg.MaxConsecutiveAgentLoops),
	}
}

func longestAgentRun(t *workflow.Trace) (int, string) {
	var (
		longest   int
		longestAg string
		curRun    int
		curAg     string
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
			longestAg = curAg
		}
	}
	return longest, longestAg
}

func (d *Detector) checkRecursion(t *workflow.Trace) *eventschema.CoachingEvent {
	for i := 1; i < len(t.Steps); i++ {
		prev := t.Steps[i-1].Prompt
		cur := t.Steps[i].Prompt
		if prev == nil || cur == nil {
			continue
		}
		if prev.PromptHash == "" || cur.PromptHash == "" {
			continue
		}
		if prev.PromptHash == cur.PromptHash {
			return &eventschema.CoachingEvent{
				WorkflowID: t.WorkflowID,
				AgentID:    cur.AgentID,
				Kind:       eventschema.CoachingKindReuseCache,
				Summary:    "Identical prompt repeated consecutively",
				Details: fmt.Sprintf(
					"Steps %d and %d share prompt hash %s. Cache the response or break the loop.",
					i-1, i, cur.PromptHash),
				ReplayMetadata: map[string]string{
					"prompt_hash": cur.PromptHash,
				},
			}
		}
	}
	return nil
}
