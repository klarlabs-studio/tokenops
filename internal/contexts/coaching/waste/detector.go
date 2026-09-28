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
	"slices"
	"strings"

	"go.klarlabs.de/tokenops/internal/contexts/workflows/workflow"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// Config tunes the detector. Zero values produce reasonable defaults
// derived from common production agent runs.
type Config struct {
	// MaxContextTokens is the absolute ceiling above which a step's
	// InputTokens triggers an OversizedContext finding. Default 32_768.
	MaxContextTokens int64
	// ContextGrowthLimitTokens flags a workflow whose total positive
	// context growth exceeds this number. Ignored when
	// ContextGrowthPerStepTokens is set; when neither is set the
	// per-step default applies.
	ContextGrowthLimitTokens int64
	// ContextGrowthPerStepTokens flags a workflow whose mean positive
	// context growth per step exceeds this number, once it has at least
	// minPerStepGrowthSteps steps. A long agent session grows a steady
	// amount per turn and compacts now and then, so its total growth
	// scales with its length and only a per-step rate separates runaway
	// growth from a long session. Zero uses the total rule.
	ContextGrowthPerStepTokens int64
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
	// CompactAtTokens is the context size past which a session should
	// compact. Stretches of at least minUncompactedSteps steps above it
	// without a compaction are reported as compact_earlier, priced as the
	// cache-read carried above the session's post-compaction size. Zero
	// disables the check; the code-agent profiles set it.
	CompactAtTokens int64
	// Profiles are operator-supplied per-workflow-prefix overrides
	// (config: coaching.context_limits). The first matching profile wins
	// and replaces the built-in ProfileFor selection for that workflow.
	Profiles []Profile
}

// Profile overrides thresholds for workflows whose ID starts with
// WorkflowPrefix. Zero fields inherit from the detector's base Config.
type Profile struct {
	WorkflowPrefix             string
	MaxContextTokens           int64
	ContextGrowthLimitTokens   int64
	ContextGrowthPerStepTokens int64
	MaxConsecutiveAgentLoops   int
	SystemRedundancyMin        int
	CompactAtTokens            int64
}

// minPerStepGrowthSteps is the fewest steps a per-step growth rate is
// judged on; over a handful of steps one large paste dominates the mean.
const minPerStepGrowthSteps = 10

// defaultContextGrowthPerStep applies when neither growth limit is set:
// on the default 32k window it means context doubling every four steps.
const defaultContextGrowthPerStep = 8_192

func (c *Config) defaults() {
	if c.MaxContextTokens <= 0 {
		c.MaxContextTokens = 32_768
	}
	if c.ContextGrowthLimitTokens <= 0 && c.ContextGrowthPerStepTokens <= 0 {
		// Judged per step: a total limit flags every workflow past a
		// certain length, whatever it does per step.
		c.ContextGrowthPerStepTokens = defaultContextGrowthPerStep
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
	if p, ok := d.operatorProfile(trace.WorkflowID); ok {
		// Operator-configured limits replace the built-in profile
		// selection entirely — they exist precisely to override it.
		cfg = mergeConfig(cfg, Config{
			MaxContextTokens:           p.MaxContextTokens,
			ContextGrowthLimitTokens:   p.ContextGrowthLimitTokens,
			ContextGrowthPerStepTokens: p.ContextGrowthPerStepTokens,
			MaxConsecutiveAgentLoops:   p.MaxConsecutiveAgentLoops,
			SystemRedundancyMin:        p.SystemRedundancyMin,
			CompactAtTokens:            p.CompactAtTokens,
		})
		// compact_at_tokens is newer than the other limits, so a profile
		// written before it existed says nothing about it; inheriting the
		// built-in value keeps the check on rather than silently off.
		if p.CompactAtTokens == 0 {
			if builtin := ProfileFor(trace.WorkflowID); builtin != nil {
				cfg.CompactAtTokens = builtin.CompactAtTokens
			}
		}
	} else if profile := ProfileFor(trace.WorkflowID); profile != nil {
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
	if ev := scoped.checkLateCompaction(trace); ev != nil {
		out = append(out, ev)
	}
	return out
}

// operatorProfile returns the first configured profile whose prefix
// matches workflowID.
func (d *Detector) operatorProfile(workflowID string) (Profile, bool) {
	for _, p := range d.cfg.Profiles {
		if p.WorkflowPrefix != "" && strings.HasPrefix(workflowID, p.WorkflowPrefix) {
			return p, true
		}
	}
	return Profile{}, false
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
	// Per-step growth limits sit at about twice the highest rate seen
	// across 30 days of real sessions (2026-09: Claude Code p99 ~2.1k,
	// Codex p99 ~7k tokens per step).
	case strings.HasPrefix(workflowID, "claude-code:"):
		return &Config{
			MaxContextTokens:           900_000,
			ContextGrowthPerStepTokens: 5_000,
			CompactAtTokens:            600_000,
		}
	case strings.HasPrefix(workflowID, "codex:"):
		return &Config{
			MaxContextTokens:           250_000,
			ContextGrowthPerStepTokens: 15_000,
			CompactAtTokens:            150_000,
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
		// An explicit total asks for the total rule; a per-step limit
		// inherited from the defaults must not shadow it.
		out.ContextGrowthPerStepTokens = 0
	}
	if profile.ContextGrowthPerStepTokens > 0 {
		out.ContextGrowthPerStepTokens = profile.ContextGrowthPerStepTokens
	}
	if profile.MaxConsecutiveAgentLoops > 0 {
		out.MaxConsecutiveAgentLoops = profile.MaxConsecutiveAgentLoops
	}
	if profile.SystemRedundancyMin > 0 {
		out.SystemRedundancyMin = profile.SystemRedundancyMin
	}
	if profile.CompactAtTokens > 0 {
		out.CompactAtTokens = profile.CompactAtTokens
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
	if limit := d.cfg.ContextGrowthPerStepTokens; limit > 0 {
		if t.StepCount < minPerStepGrowthSteps {
			return nil
		}
		intervals := int64(t.StepCount - 1)
		perStep := t.ContextGrowthTotal / intervals
		if perStep < limit {
			return nil
		}
		return &eventschema.CoachingEvent{
			WorkflowID: t.WorkflowID,
			Kind:       eventschema.CoachingKindTrimContext,
			Summary:    "Runaway context growth",
			Details: fmt.Sprintf(
				"Context grew %d tokens per step on average across %d steps (limit %d). Each step is appending more than it should.",
				perStep, t.StepCount, limit),
			EstimatedSavingsTokens: (perStep - limit) * intervals,
		}
	}
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
	// One agent monopolising a workflow only means something when the
	// workflow has other agents to hand to. In a single-agent session
	// every turn is the same agent and the run length is the session
	// length; repetition there is checkRecursion's identical-prompt rule.
	if distinctAgents(t) < 2 {
		return nil
	}
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

// distinctAgents counts the named agents that took a step in t.
func distinctAgents(t *workflow.Trace) int {
	seen := map[string]struct{}{}
	for _, step := range t.Steps {
		if step.Prompt != nil && step.Prompt.AgentID != "" {
			seen[step.Prompt.AgentID] = struct{}{}
		}
	}
	return len(seen)
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

// minUncompactedSteps is the shortest stretch above CompactAtTokens that is
// reported. A few turns past the line is a session finishing a task; tens
// of turns is a habit.
const minUncompactedSteps = 20

// compactionDrop is how far context must fall between two steps to count
// as a compaction or a fresh start rather than ordinary variation.
const compactionDrop = 0.5

// checkLateCompaction reports stretches that ran above CompactAtTokens
// without compacting, priced against what compacting at the threshold
// would have carried instead.
//
// Every step re-reads the whole context. Compacting at the threshold does
// not hold context at the size a compaction leaves: it grows back and is
// compacted again, a sawtooth whose mean is halfway between that size and
// the threshold. The excess is each step's context above that mean. The
// post-compaction size is this session's own when it has compacted, and an
// eighth of the threshold when it never has (the median across 30 days of
// real Claude Code sessions, 77k at a 600k threshold).
func (d *Detector) checkLateCompaction(t *workflow.Trace) *eventschema.CoachingEvent {
	limit := d.cfg.CompactAtTokens
	if limit <= 0 || t.MaxContextSize < limit {
		return nil
	}
	base := postCompactionBaseline(t.Steps, limit/8)
	mean := (base + limit) / 2
	var (
		run, longest, stretched int
		excessTokens            int64
		excessUSD               float64
	)
	for i, s := range t.Steps {
		if s.Prompt == nil {
			continue
		}
		ctx := s.Prompt.InputTokens
		if i > 0 && compacted(t.Steps[i-1], s) {
			run = 0
		}
		if ctx < limit {
			// Dipping below the line without compacting leaves the
			// stretch open: it is the same uncompacted context.
			continue
		}
		run++
		longest = max(longest, run)
		stretched++
		excessTokens += ctx - mean
		excessUSD += s.ListCostUSD * float64(ctx-mean) / float64(ctx)
	}
	if longest < minUncompactedSteps {
		return nil
	}
	return &eventschema.CoachingEvent{
		WorkflowID: t.WorkflowID,
		Kind:       eventschema.CoachingKindCompactEarlier,
		Summary:    "Long stretches near the context ceiling without compacting",
		Details: fmt.Sprintf(
			"%d steps ran above %s context (longest stretch %d steps without a compaction). "+
				"Compacting at that line would have kept context near %s on average; these steps re-read about %s more%s. "+
				"Compact, or start a fresh session, once context passes %s.",
			stretched, humanTokens(limit), longest, humanTokens(mean), humanTokens(excessTokens),
			usdClause(excessUSD), humanTokens(limit)),
		EstimatedSavingsTokens: excessTokens,
		EstimatedSavingsUSD:    excessUSD,
		ReplayMetadata: map[string]string{
			"compact_at_tokens": fmt.Sprint(limit),
			"steps_above":       fmt.Sprint(stretched),
			"longest_stretch":   fmt.Sprint(longest),
			"baseline_tokens":   fmt.Sprint(base),
			"mean_tokens":       fmt.Sprint(mean),
		},
	}
}

// compacted reports whether context fell enough from prev to cur to be a
// compaction or a fresh start.
func compacted(prev, cur workflow.Step) bool {
	if prev.Prompt == nil || cur.Prompt == nil {
		return false
	}
	return float64(cur.Prompt.InputTokens) < float64(prev.Prompt.InputTokens)*compactionDrop
}

// postCompactionBaseline is the median context right after this trace's
// compactions, or fallback when it has none.
func postCompactionBaseline(steps []workflow.Step, fallback int64) int64 {
	var after []int64
	for i := 1; i < len(steps); i++ {
		if compacted(steps[i-1], steps[i]) {
			after = append(after, steps[i].Prompt.InputTokens)
		}
	}
	if len(after) == 0 {
		return fallback
	}
	slices.Sort(after)
	return after[len(after)/2]
}

func humanTokens(n int64) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM tokens", float64(n)/1e6)
	case n >= 1_000:
		return fmt.Sprintf("%dk tokens", n/1_000)
	}
	return fmt.Sprintf("%d tokens", n)
}

func usdClause(usd float64) string {
	if usd < 0.01 {
		return ""
	}
	return fmt.Sprintf(" (~$%.2f at API rates; on a flat plan, that share of its allowance)", usd)
}
